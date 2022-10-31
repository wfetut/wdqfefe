---
authors: Michael Wilson (mike@goteleport.com)
state: draft
---

# RFD 95 - GCP Console and CLI Support

## What

Teleport currently supports the AWS CLI through Teleport through the use of:

`tsh aws <subcommands>`

We want to introduce a similar functionality for GCP, where we're able to issue
`gcloud` commands through the use of:

`tsh gcloud <subcommands>`

Additionally, we want to allow users to access the GCP console similar to the way
that Teleport allows access to the AWS console.

## Why

In order to expand our application access offering, we need to ensure we support
the various cloud providers that our customers are using.

## Details

Identity federation is possible on Google Cloud through the use of the [Directory API](https://developers.google.com/admin-sdk/directory/) and [SAML](https://cloud.google.com/architecture/identity/single-sign-on). The expectation here is that [Google identities](https://cloud.google.com/architecture/identity/single-sign-on) are established for federated users and SAML is used to provide a single sign-on.

### An example: federated identity on Google Cloud through Google Directory Sync and SAML

Google Cloud has [loose support for identity federation from LDAP](https://cloud.google.com/architecture/identity/single-sign-on) via the use of what appears
to be a closed source tool called [Google Cloud Directory Sync](https://cloud.google.com/architecture/identity/single-sign-on) (GCDS). This tool monitors
an Active Directory or other LDAP implementation and uses Google Cloud APIs to
establish a mapping between LDAP and identities within Google Cloud.

Internally [GCDS uses](https://cloud.google.com/architecture/identity/single-sign-on) the [Directory API](https://developers.google.com/admin-sdk/directory/) and the [Domain Shared Contacts API](https://developers.google.com/google-apps/domain-shared-contacts/) in order to map identities from the active directory to Google Cloud.
From here, Google Cloud supports SAML for logging in, which can be used in conjunction with GCDS to establish a single sign on mechanism.

### Implementing GCP federation in Teleport

#### Assigning a GCP admin user to Teleport

A service account with permission to use the Admin SDK must be created. This step will have to be performed by the user. First, the [admin SDK must be enabled](https://console.cloud.google.com/flows/enableapi?apiid=admin.googleapis.com), and then a service account must be created with permissions to use the admin SDK. The credentials for this service account must be stored in Teleport. A new field should be added to `app` entries in `teleport.yaml`:

```yaml
...
app_service:
    enabled: yes
    apps:
    - name: "gcpconsole"
      uri: "http://console.cloud.google.com"
      public_addr: "gcpconsole.teleport"
      gcp_credentials: /path/to/credentials.json
```

Multiple consoles could be supported through the use of different credentials:

```yaml
app_service:
    enabled: yes
    apps:
    - name: "gcpconsole-prod"
      uri: "http://console.cloud.google.com"
      public_addr: "gcpconsole.teleport"
      gcp_credentials: /path/to/credentials-prod.json
    - name: "gcpconsole-dev"
      uri: "http://console.cloud.google.com"
      public_addr: "gcpconsole.teleport"
      gcp_credentials: /path/to/credentials-dev.json
```

Though due to the structure of Google Cloud, supported this may be unnecessary.

#### Provisioning Teleport users in Google Cloud

Once one or more GCP consoles have been created in application access, an identity synchronization service should run periodically that takes Teleport users and provisions them on GCP. This synchronization service should additionally run on user or role modifications at the Teleport level.


#### GCP role synchronization

The user and role objects should get new fields called `GCPRoles` which allows administrators to set the GCP roles that a user or role has access to. When a user is synchronized to GCP, the user will have a list of roles amalgamated from the Teleport user and role objects assigned to them.

There does not appear to be a way to have an IAM AssumeRole-like functionality for GCP where a user is only able to use the specified roles assigned to them. We could potentially emulate this, but I'll defer the exerise of designing that until there's interest.

#### GCP CLI access

To enable GCP CLI access, the user must issue the following command from the command line:

```
tsh app login gcpconsole
```

where `gcpconsole` is the name of the GCP console the user is attempting to log into. This will trigger the `gcloud auth login` in as non-interactive a way as possible. From there, users will be able to issue gcloud commands like the following:

```
tsh gcloud ...
```

#### Required Approvers

The purpose of the `Required Approvers` section is to be explicit on required
and optional approvers for an RFD. For the subject matter experts that can
provide high quality feedback to help refine and improve the RFD.

For example, suppose you are making a change with internal implementation
changes, security relevant changes, with also product changes (new fields,
flags, or behavior). You might create a `Required Approvers` section that looks
something like the following.

```
# Required Approvers
* Engineering @zmb3 && (@codingllama || @nklaassen )
* Security @reed
* Product: (@xinding33 || @klizhentas )
```

### Security

Describe the security considerations for your design doc.
(Non-exhaustive list below.)

* Explore possible attack vectors, explain how to prevent them
* Explore DDoS and other outage-type attacks
* If frontend, explore common web vulnerabilities
* If introducing new attack surfaces (UI, CLI commands, API or gRPC endpoints),
  consider how they may be abused and how to prevent it
* If introducing new auth{n,z}, explain their design and consequences
* If using crypto, show that best practices were used to define it

### UX

Describe the UX changes and impact of your design doc.
(Non-exhaustive list below.)

* Explore UI, CLI and API user experience by diving through common scenarios
  that users would go through
* Show UI, CLI and API requests/responses that the user would observe
* Make error messages actionable, explore common failure modes and how users can
  recover
* Consider the UX of configuration changes and their impact on Teleport upgrades
* Consider the UX scenarios for Cloud users

## References
- [Overview of Identity and Access Management](https://cloud.google.com/architecture/identity)
- [Implementing Federation](https://cloud.google.com/architecture/identity/federating-gcp-with-active-directory-introduction#implementing_federation)