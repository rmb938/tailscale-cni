# -*- mode: Python -*-

# set defaults
settings = {
    'default_registry': 'localhost'
}

allow_k8s_contexts('kubernetes-admin@kubernetes')

# global settings
settings.update(read_json(
    "tilt-settings.json",
    default={},
))

default_registry(settings['default_registry'])

def deploy_tailscale_cni():

    custom_build(
        'tailscale-cni',
        'docker build -t $EXPECTED_REF .',
        deps=[
            'bin/tailscale-cni-linux-amd64',
            'Dockerfile'
        ],
    )

    yaml = str(kustomize("kustomize/tilt"))
    substitutions = settings.get("kustomize_substitutions", {})
    for substitution in substitutions:
        value = substitutions[substitution]
        yaml = yaml.replace("${" + substitution + "}", value)
    k8s_yaml(blob(yaml))


# Users may define their own Tilt customizations in tilt.d. This directory is excluded from git and these files will
# not be checked in to version control.
def include_user_tilt_files():
    user_tiltfiles = listdir("tilt.d")
    for f in user_tiltfiles:
        include(f)


##############################
# Actual work happens here
##############################
include_user_tilt_files()

deploy_tailscale_cni()
