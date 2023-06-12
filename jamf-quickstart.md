# Jamf Service quickstart

Branches:

* https://github.com/gravitational/teleport/tree/codingllama/jamf-live1
* https://github.com/gravitational/teleport.e/tree/codingllama/jamf1-e

    The OSS branch references the e/ commits.

    The Ent branch contains a superset of
    https://github.com/gravitational/teleport.e/pull/1587,
    https://github.com/gravitational/teleport.e/pull/1589 and additionally sets
    `devicetrust.MDMFeatureActive=true` (the feature flag will be dropped before
    13.2).

1. Start Teleport Enterprise Auth+Proxy servers, have at least one user
   configured.

    No particular settings required from Auth/Proxy, Device Trust/MDM defaults
    are good enough.

2. Configure the Jamf service as a separate Teleport Enterprise process

    Example configuration:

    ```yaml
    version: v3
    teleport:
      nodename: jamf
      data_dir: /Users/alan/telehome/datadir-jamf
      proxy_server: zarquone.dev:3080

    jamf_service:
      enabled: true
      sync_delay: -1  # Start syncs immediately
      api_endpoint: https://teleporttest.jamfcloud.com      # CHANGEME
      username: llama                                       # CHANGEME
      password_file: /Users/alan/telehome/jamf_password.txt # CHANGEME
      inventory:
      - sync_period_partial: 2m
        sync_period_full: 10m
        on_missing: "DELETE"

    auth_service:
      enabled: false

    proxy_service:
      enabled: false

    ssh_service:
      enabled: false
    ```

3. Create an MDM join token

    `tctl tokens add --type=mdm`

4. Start the Jamf service

    `teleport start -d -c /path/to/jamf/teleport.yaml` --token=TOKEN_FROM_STEP_3

    That's it.

    Syncs should start presently and synced devices should appear on Teleport
    (`tctl devices ls`).
