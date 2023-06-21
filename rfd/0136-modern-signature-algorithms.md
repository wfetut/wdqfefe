---
authors: Nic Klaassen (nic@goteleport.com)
state: draft
---

# RFD 136 - Modern Signature Algorithms

## Required Approvers

TODO

## What

Teleport should support modern key types and signature algorithms, currently
only RSA2048 keys are supported with the PKCS#1 v1.5 signature scheme.
This applies to CA keys and client (user/host) keys, but each can/will be
addressed individually.

## Why

Modern algorithms like ECDSA and Ed25519 offer better security properties with
smaller keys that are faster to generate and sign with.
Some of the more restrictive security policies are starting to reject RSA2048
(e.g. [RHEL 8's FUTURE policy](https://access.redhat.com/articles/3642912)).

## Details

### Algorithms

These algorithms are being considered for support:

#### RSA

Private key sizes: 2048, 3072, 4096

Signature algorithms: PKCS#1 v1.5, (maybe PSS for TLS and JWT? SSH does not support it)

Digest/hash algorithms: SHA512 for SSH, SHA256 for TLS

Considerations:

* RSA2048 is the current default and deviating from it by default may break
  compatibility with third-party components and protocols
* RSA has the most widespread support among all protocols
* Certain database protocols only support RSA client certs
  * <https://docs.snowflake.com/en/user-guide/key-pair-auth#step-2-generate-a-public-key>
* If we must continue to support RSA, we might as well support larger key sizes,
  3072 and 4096-bit are the most commonly used and supported by e.g. GCP KMS.
* golang.org/x/crypto/ssh always uses SHA512 hash with RSA public keys
  <https://github.com/golang/crypto/blob/0ff60057bbafb685e9f9a97af5261f484f8283d1/ssh/certs.go#L443-L445>
* crypto/x509 always uses SHA256 hash with RSA public keys
  <https://github.com/golang/go/blob/dbf9bf2c39116f1330002ebba8f8870b96645d87/src/crypto/x509/x509.go#L1411-L1414>
* ssh only supports the PKCS#1 v1.5 signature scheme
  <https://datatracker.ietf.org/doc/html/rfc8332>
* FIPS 186-5 approves all listed options
* BoringCrypto supports all listed options

#### ECDSA

Curves: P-256, (maybe P-384, P-521?)

Digest/hash algorithms: SHA256, (or SHA384, SHA512 for P-384, P-521)

Considerations:

* ECDSA has good support across SSH and TLS protocols for both client and CA
  certs.
* ECDSA key generation is *much* faster than RSA key generation.
* ECDSA signatures are faster than RSA signatures.
* FIPS 186-5 approves all listed options
* BoringCrypto supports all listed options

#### EdDSA

Curves: Ed25519 (this is the only curve supported in Go)

Digest/hash algorithms: none (the full message is signed without hashing)

Considerations:

* There is widespread support for Ed25519 SSH certs.
* Go libraries support Ed25519 for TLS
* Support for Ed25519 is *not* widespread in the TLS ecosystem.
* YubiHSM and GCP KMS do *not* support Ed25519 keys.
* Ed25519 is considered by some to be the fastest, most secure, most modern
  option for SSH certs.
* Ed25519 key generation is *much* faster than RSA key generation.
* Ed25519 signatures are faster than RSA signatures.
* FIPS 186-5 approves Ed25519
* BoringCrypto does not support Ed25519
  <https://go.googlesource.com/go/+/dev.boringcrypto/src/crypto/tls/boring.go#80>

#### Summary

* We are probably forced to continue unconditionally using RSA for database
  certs, I'm assuming this would apply to both client and CA.
* Ed25519 is a modern favourite for SSH, but TLS (and HSM, KMS) support is lacking.
* Teleport derives client SSH and TLS certs from the same client keypair,
  supporting different algorithms for each would require larger product changes.
* Teleport CAs use separate keypairs for SSH and TLS, they do not need to use
  the same algorithm.
* Overall it looks like ECDSA with the P-256 curve is a secure, modern option
  with good performance and good support for all protocols (except some databases).

### CAs

Each Teleport CA holds 1 or more of the following:

* SSH public and private key
* TLS certificate and private key
* JWT public and private key

Each CA key may be a software key stored in the Teleport backend, an HSM key
held in an HSM connected to a local Auth server via a PKCS#11 interface, or a
KMS key held in GCP KMS.

Teleport currently has these CAs:

#### User CA

keys: ssh, tls

uses: user ssh cert signing, user tls cert signing, ssh hosts trust this CA

current algo: `RSA2048_PKCS1_SHA(256|512)`

proposed default algo: `ECDSA_P256_SHA256`? For both SSH and TLS

#### Host CA

keys: ssh, tls

uses: host ssh cert signing, host tls cert signing, ssh clients trust this CA

current algo: `RSA2048_PKCS1_SHA(256|512)`

proposed default algo: `ECDSA_P256_SHA256`? For both SSH and TLS

#### Database CA

keys: tls

uses: user db tls cert signing, dbs trust this CA

current algo: `RSA2048_PKCS1_SHA256`

proposed default algo: RSA2048?

#### JWT CA

keys: jwt

uses: user jwt cert signing, exposed at `/.well-known/jwks.json`, applications that verify user JWTs trust this CA

current algo: `RSA2048_PKCS1_SHA256`

proposed default algo: `ECDSA_P256_SHA256`?

#### OIDC IdP CA

keys: jwt

uses: TODO

current algo: RSA2048

proposed default algo: `ECDSA_P256_SHA256`?

#### SAML IdP CA

keys: tls

uses: TODO

current algo: RSA2048

proposed default algo: `ECDSA_P256_SHA256`?

### CA Configuration

We have a few options for how to evolve the algorithms used for CA keys and
signatures.

#### 1. Don't make it configurable

We can just pick a new algorithm to use universally (one for each CA).
This will be used for all new clusters.
It will also be used for new keys the next time that existing clusters do a CA
rotation.

#### 2. Make it configurable via `cluster_auth_preference` and `teleport.yaml`

Why both?
We probably don't want it configurable only via `cluster_auth_preference` so
that you can start a new cluster and the ca keys will be automatically generated
at first start with the correct algorithms, so you don't have to immediately
edit the `cap` and then rotate all of your brand-new CAs.
We probably don't want it configurable only via `teleport.yaml` so that it can
be configurable for Cloud.

```yaml
kind: cluster_auth_preference
metadata:
  name: cluster-auth-preference
spec:
  ca_key_params:
    host:
      ssh:
        algorithm: ECDSA_P256_SHA256 # users can select one of our chosen supported algorithms
      tls:
        algorithm: ECDSA_P256_SHA256
    user:
      ssh:
        algorithm: Ed25519
      tls:
        # Maybe SSH and TLS can mismatch? Or we block this for now but leave the door open for the future
        algorithm: ECDSA_P256_SHA256
    jwt:
      jwt:
        # You can choose "recommended" to let Teleport automatically pick the
        # algorithm, and it may automatically change in the future during a user-initiated
        # CA rotation.
        algorithm: recommended
    db:
      tls:
        # likely that people will want to stick with RSA for DB access compat
        algorithm: RSA2048_PKCS1_SHA256

    # We should (probably) offer a user configurable default for two reasons:
    # 1. so users don't have to list out every single CA type
    # 2. so we can follow it when new CA types are added
    default:
      ssh:
        algorithm: recommended # let Teleport choose
      tls:
        algorithm: ECDSA_P256_SHA256 # always use this for TLS keys
      jwt:
        algorithm: recommended
```

```yaml
# teleport.yaml
version: v3
auth_service:
  enabled: true
  ca_key_params:
    gcp_kms:
      keyring: projects/teleport-dev-320620/locations/us-west1/keyRings/nic-testplan-13
      protection_level: "SOFTWARE"
    default:
      ssh:
        algorithm: recommended
      tls:
        algorithm: recommended
      jwt:
        algorithm: recommended
    host:
      ssh:
        algorithm: ECDSA_P256_SHA256
      tls:
        algorithm: ECDSA_P256_SHA256
    user:
      ssh:
        algorithm: Ed25519
      tls:
        algorithm: ECDSA_P256_SHA256
    db:
      tls:
        algorithm: RSA2048
    jwt:
      jwt:
        algorithm: recommended # let Teleport choose
```

#### Cloud

Cloud will be able to select their preferred defaults by configuring them in the
`teleport.yaml`

Should CA algorithms be configurable by Cloud users?
I don't see why not, we will only support secure algorithms.
They can choose their own algorithms be editing the `cluster_auth_preference`
and doing a CA rotation.

### Subjects

It will be nice to update the CA key algorithms for security and performance
benefits, but what users really see on a day-to-day basis is their user keys.

"Subjects" that have certificates issued by the Teleport CAs include:

* Teleport users via `tsh login`
* Teleport users via `tsh app login`
* Teleport users via `tsh db login`
* Teleport users via Teleport Connect
* Teleport services (ssh, app, db, kube, windows desktop, etc)
* Machine ID (`tbot`)
* Teleport Plugins
* OpenSSH hosts

All of these currently generate an RSA2048 keypair locally, send the public key
to the auth server, and receive signed certificates of some variety.

We have a few options for how to evolve this:

1. Choose a new unconfigurable algorithm to use for each client.
2. Try to match the algorithm of the relevant CA for each.
3. Make each configurable cluster-wide. (this is similar to (2) but doesn't
   require subject and CA algorithms to match).
4. Make each configurable by each subject (like `tsh login --keytype ecdsa_P256`, new field in `teleport.yaml` for hosts, etc).

My personal opinion: The MVP is (2).
(1) is not flexible enough, (2) seems like it would cover >80% of cases, (3)
adds a few more knobs without major benefits, someone will eventually ask for
(4) and we can consider adding it then.

### Backward Compatibility

* Will the change impact older clients? (tsh, tctl)

Auth servers with non-default algorithms configured should continue to sign
certificates for clients on older Teleport versions using RSA2048 keys.

Open questions:

* Should we start rejecting RSA2048 (if a different algo is configured) in
  future Teleport versions (V15)?
* Should we make some configurable `allowed_subject_algorithms` per CA to put
  this in the hands of the user?

* Are there any backend migrations required?

A CA rotation will effectively act as the backend migration when changing
algorithms.

TODO:

* What impact does the change have on remote clusters?
* How will changes be rolled out across future versions?

### Security

TODO:

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

TODO:

Describe the UX changes and impact of your design doc.
(Non-exhaustive list below.)

* Explore UI, CLI and API user experience by diving through common scenarios
  that users would go through
* Show UI, CLI and API requests/responses that the user would observe
* Make error messages actionable, explore common failure modes and how users can
  recover
* Consider the UX of configuration changes and their impact on Teleport upgrades
* Consider the UX scenarios for Cloud users

### Proto Specification

TODO:

Include any `.proto` changes or additions that are necessary for your design.

### Audit Events

TODO:

Include any new events that are required to audit the behavior
introduced in your design doc and the criteria required to emit them.

### Observability

TODO:

Describe how you will know the feature is working correctly and with acceptable
performance. Consider whether you should add new Prometheus metrics, distributed
tracing, or emit log messages with a particular format to detect errors.

### Product Usage

TODO:

Describe how we can determine whether the feature is being adopted. Consider new
telemetry or usage events.

### Test Plan

TODO:

Include any changes or additions that will need to be made to
the [Test Plan](../.github/ISSUE_TEMPLATE/testplan.md) to appropriately
test the changes in your design doc and prevent any regressions from
happening in the future.
