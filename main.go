package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"github.com/vishvananda/netlink"
	"inet.af/netaddr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"tailscale.com/client/tailscale"
	"tailscale.com/ipn"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
}

func main() {
	var healthAddr string
	var metricsAddr string

	var tailscaleSyncDuration time.Duration
	var routeTableSyncDuration time.Duration
	var iptableSyncDuration time.Duration

	flag.StringVar(&healthAddr, "health-addr", ":8081", "The address the health endpoints binds to.")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")

	flag.DurationVar(&tailscaleSyncDuration, "tailscale-sync-duration", 5*time.Minute, "The duration to sync tailscale configuration")
	flag.DurationVar(&routeTableSyncDuration, "route-table-sync-duration", 5*time.Minute, "The duration to sync route table configuration")
	flag.DurationVar(&iptableSyncDuration, "iptable-sync-duration", 5*time.Minute, "The duration to sync iptable configuration")

	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: healthAddr,
		LeaderElection:         false,
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	err = mgr.AddReadyzCheck("ping", healthz.Ping)
	if err != nil {
		setupLog.Error(err, "unable to add readyz check")
		os.Exit(1)
	}

	err = mgr.AddHealthzCheck("ping", healthz.Ping)
	if err != nil {
		setupLog.Error(err, "unable to add healthz check")
		os.Exit(1)
	}

	signalHandler := ctrl.SetupSignalHandler()

	// cni config runnable
	err = mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		c := mgr.GetClient()

		nodeName := os.Getenv("NODE_NAME")
		node := &corev1.Node{}
		err := c.Get(ctx, types.NamespacedName{Name: nodeName}, node)
		if err != nil {
			setupLog.Error(err, "error getting node", "node", nodeName)
			return err
		}

		return InsertPodCidrInCniSpec("/etc/cni/net.d/10-tailscale.conflist", node.Spec.PodCIDR)
	}))

	// tailscale runnable
	err = mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		ticker := time.NewTicker(tailscaleSyncDuration)

		doTailscale(mgr.GetClient(), ctx)

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				doTailscale(mgr.GetClient(), ctx)
			}
		}
	}))
	if err != nil {
		setupLog.Error(err, "error setting up tailscale runnable")
		os.Exit(1)
	}

	// route runnable
	err = mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		ticker := time.NewTicker(routeTableSyncDuration)

		doRouteTable(mgr.GetClient(), ctx)

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				doRouteTable(mgr.GetClient(), ctx)
			}
		}
	}))
	if err != nil {
		setupLog.Error(err, "error setting up route table runnable")
		os.Exit(1)
	}

	// iptable runnable
	err = mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		ticker := time.NewTicker(routeTableSyncDuration)

		doIptables(mgr.GetClient(), ctx)

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				doIptables(mgr.GetClient(), ctx)
			}
		}
	}))
	if err != nil {
		setupLog.Error(err, "error setting up iptable runnable")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(signalHandler); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func doTailscale(c client.Client, ctx context.Context) {
	nodeName := os.Getenv("NODE_NAME")
	node := &corev1.Node{}
	err := c.Get(ctx, types.NamespacedName{Name: nodeName}, node)
	if err != nil {
		setupLog.Error(err, "error getting node", "node", nodeName)
		return
	}

	prefs, err := tailscale.GetPrefs(ctx)
	if err != nil {
		setupLog.Error(err, "unable to get tailscale prefs")
		return
	}

	prefs = prefs.Clone()

	prefs.AdvertiseRoutes = []netaddr.IPPrefix{netaddr.MustParseIPPrefix(node.Spec.PodCIDR)}
	prefs.RouteAll = true

	justEditMP := new(ipn.MaskedPrefs)
	justEditMP.Prefs = *prefs
	justEditMP.WantRunningSet = true
	justEditMP.RouteAllSet = true
	justEditMP.AdvertiseRoutesSet = true

	setupLog.Info("updating prefs")
	_, err = tailscale.EditPrefs(ctx, justEditMP)
	if err != nil {
		setupLog.Error(err, "error updating prefs")
		return
	}
}

func doRouteTable(c client.Client, ctx context.Context) {
	nodeName := os.Getenv("NODE_NAME")

	tailscaleLink, err := netlink.LinkByName("tailscale0")
	if err != nil {
		setupLog.Error(err, "error getting tailscale link")
		return
	}
	tailscaleAddrs, err := netlink.AddrList(tailscaleLink, netlink.FAMILY_V4)
	if err != nil {
		setupLog.Error(err, "error getting tailscale addrs")
		return
	}

	routes, err := netlink.RouteList(tailscaleLink, netlink.FAMILY_V4)
	if err != nil {
		setupLog.Error(err, "error listing routes")
		return
	}

	nodes := &corev1.NodeList{}
	err = c.List(ctx, nodes)
	if err != nil {
		setupLog.Error(err, "error getting nodes")
		return
	}

	var toRemove []netlink.Route
	var toAdd []netlink.Route

	for _, route := range routes {
		found := false
		for _, node := range nodes.Items {
			ipnet, _ := netlink.ParseIPNet(node.Spec.PodCIDR)
			nodeRoute := netlink.Route{
				Dst: ipnet,
				Gw:  tailscaleAddrs[0].IP,
			}
			if route.Equal(nodeRoute) {
				found = true
				break
			}
		}

		if found == false {
			toRemove = append(toRemove, route)
		}
	}

	for _, node := range nodes.Items {
		if node.Name == nodeName {
			// don't add route for self
			continue
		}

		found := false

		ipnet, _ := netlink.ParseIPNet(node.Spec.PodCIDR)
		nodeRoute := netlink.Route{
			Dst: ipnet,
			Gw:  tailscaleAddrs[0].IP,
		}

		for _, route := range routes {
			if route.Equal(nodeRoute) {
				found = true
				break
			}
		}

		if found == false {
			toAdd = append(toAdd, nodeRoute)
		}
	}

	for _, route := range toRemove {
		err := netlink.RouteDel(&route)
		if err != nil {
			setupLog.Error(err, "error removing route")
		}
	}

	for _, route := range toAdd {
		err := netlink.RouteAdd(&route)
		if err != nil {
			setupLog.Error(err, "error adding route")
		}
	}
}

func doIptables(c client.Client, ctx context.Context) {
	nodeName := os.Getenv("NODE_NAME")
	node := &corev1.Node{}
	err := c.Get(ctx, types.NamespacedName{Name: nodeName}, node)
	if err != nil {
		setupLog.Error(err, "error getting node", "node", nodeName)
		return
	}

	iptablesCmd, err := iptables.NewWithProtocol(iptables.ProtocolIPv4)
	if err != nil {
		setupLog.Error(err, "error creating iptables helper")
		return
	}
	comment := "allow outbound traffic from pods"
	args := []string{"-m", "comment", "--comment", comment, "-i", "kube-bridge", "-j", "ACCEPT"}
	exits, err := iptablesCmd.Exists("filter", "FORWARD", args...)
	if err != nil {
		setupLog.Error(err, "error checking if iptables rule exists", "comment", comment)
		return
	}
	if exits == false {
		err := iptablesCmd.Insert("filter", "FORWARD", 1, args...)
		if err != nil {
			setupLog.Error(err, "error inserting iptables rule", "comment", comment)
			return
		}
	}

	comment = "allow inbound traffic from pods"
	args = []string{"-m", "comment", "--comment", comment, "-o", "kube-bridge", "-j", "ACCEPT"}
	exits, err = iptablesCmd.Exists("filter", "FORWARD", args...)
	if err != nil {
		setupLog.Error(err, "error checking if iptables rule exists", "comment", comment)
		return
	}
	if exits == false {
		err := iptablesCmd.Insert("filter", "FORWARD", 1, args...)
		if err != nil {
			setupLog.Error(err, "error inserting iptables rule", "comment", comment)
			return
		}
	}

	comment = "pod egress"
	args = []string{"-m", "comment", "--comment", comment, "-s", node.Spec.PodCIDR, "!", "-o", "kube-bridge", "-j", "MASQUERADE"}
	err = iptablesCmd.AppendUnique("nat", "POSTROUTING", args...)
	if err != nil {
		setupLog.Error(err, "error inserting iptables rule", "comment", comment)
		return
	}
}

func InsertPodCidrInCniSpec(cniConfFilePath string, cidr string) error {
	file, err := ioutil.ReadFile(cniConfFilePath)
	if err != nil {
		return fmt.Errorf("failed to load CNI conf file: %s", err.Error())
	}
	var config interface{}
	if strings.HasSuffix(cniConfFilePath, ".conflist") {
		err = json.Unmarshal(file, &config)
		if err != nil {
			return fmt.Errorf("failed to parse JSON from CNI conf file: %s", err.Error())
		}
		updatedCidr := false
		configMap := config.(map[string]interface{})
		for key := range configMap {
			if key != "plugins" {
				continue
			}
			// .conflist file has array of plug-in config. Find the one with ipam key
			// and insert the CIDR for the node
			pluginConfigs := configMap["plugins"].([]interface{})
			for _, pluginConfig := range pluginConfigs {
				pluginConfigMap := pluginConfig.(map[string]interface{})
				if val, ok := pluginConfigMap["ipam"]; ok {
					valObj := val.(map[string]interface{})
					valObj["subnet"] = cidr
					updatedCidr = true
					break
				}
			}
		}

		if !updatedCidr {
			return fmt.Errorf("failed to insert subnet cidr into CNI conf file: %s as CNI file is invalid", cniConfFilePath)
		}

	} else {
		err = json.Unmarshal(file, &config)
		if err != nil {
			return fmt.Errorf("failed to parse JSON from CNI conf file: %s", err.Error())
		}
		pluginConfig := config.(map[string]interface{})
		pluginConfig["ipam"].(map[string]interface{})["subnet"] = cidr
	}
	configJSON, _ := json.Marshal(config)
	err = ioutil.WriteFile(cniConfFilePath, configJSON, 0644)
	if err != nil {
		return fmt.Errorf("failed to insert subnet cidr into CNI conf file: %s", err.Error())
	}
	return nil
}
