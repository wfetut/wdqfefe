---
authors: Walt D. (walt@goteleport.com)
state: draft
---

# RFD 00XX - Mirroring Artifacts to 3rd Party Repos

## What

I propose we mirror Teleport artifacts to _some_ 3rd party repositories
and the criteria for selecting those repositories.

Additionally, I propose guideline that we do not use 3rd party repositories
as the install source in our documentation. First party artifact hosting remains
the rule and 3rd party hosting should be called out rarely as the exception.


## Why

Our users deploy Teleport via a variety of technologies.  Some use docker (OCI)
images, deployed via a Kubernetes Helm chart.  Others use Terraform to deploy
Teleport via AMIs.  Many of these technologies provide _technology_ specific
tools and repositories for distributing build artifacts. For example:

 - Hashicorp provides https://registry.terraform.io/ as its sanctioned source
   of both Terraform providers and modules.
 - Docker provides https://hub.docker.com/ as the default registry which the
   docker client looks for images in.
 - Amazon provides a public Community AMI directory.

Our users regularly ask that we publish artifacts to a variety of 3rd party repos.
For example:

 - A [request to publish our Terraform provider](https://github.com/gravitational/teleport-plugins/issues/235) to https://registry.terraform.io/
 - A [request to publish our OCI images to Docker Hub](https://github.com/gravitational/teleport/issues/4159)
 - A [request to publish our AMIs to GovCloud](https://github.com/gravitational/teleport/issues/10026)

These requests are motivated by ease-of-use as well as security concerns like
"we only want to install X that is vouched for by the vendor".

Furthermore, without sanctioned Teleport artifacts available in these 3rd party
repositories, our community fills in the gap with their own Teleport artifacts.
For example:

 - A [Terraform provider](https://registry.terraform.io/providers/pzduniak/teleport/latest) from https://github.com/pzduniak/terraform-provider-teleport
 - The [skyscrapers/teleport OCI image](https://hub.docker.com/r/skyscrapers/teleport) on Docker Hub

When a user searches for "teleport" on one of these 3rd party repos, the community
sourced images are the first results, and can achieve notable usage.
`skyscrapers/teleport` has 500,000+ downloads as of 2022-03.

These community maintained 3rd party artifacts present security risks:

 - Teleport does not own the supply chain for these artifacts. We can't make
   any promises about their current for future security properties.
 - The artifacts are often out of date.  At best, they update within a couple
   hours of when we promote.  A couple days or weeks is more typical. The
   skyscrapers/teleport image mentioned above defaults to 6.0.2 -- though it does
   offer more current tags.

```
$ date && docker run --rm -it --entrypoint teleport skyscrapers/teleport version
Mon 14 Mar 2022 05:41:52 PM PDT
Teleport v6.0.2 git:v6.0.2-0-g1cb1420b7 go1.15.5
```


## Details

In order to meet our users where they're comfortable, and thus speed up
deployment of Teleport and time to first value, I propose we mirror artifacts
to some of these default locations.

The purpose of this RFD is not to exhaustively list all 3rd party repositories
that publish bootleg Teleport artifacts. While I speak to some specific
repositories, I aim to foster a discussion and establish a guideline.  Either:

 - We decide we will publish to 3rd party repos, and establish criteria for
   selecting them and guidelines for maintaining them.
 - We decide we won't publish to 3rd party repos.

Either way, this RFD will serve as a record we can point users and maintainers
to when they
request Teleport is hosted in a 3rd party repos.

### Definitions
The following terminology is useful to understand the design:

**Artifact** - An artifact is a consumable product of source code. For example:
* A Teleport binary tarball downloaded from https://goteleport.com/downloads
* A Teleport OCI image from https://quay.io/repository/gravitational/teleport
* A Helm chart from https://charts.releases.teleport.dev

**OCI image** - A.k.a. A Docker image or container image. Open Container
Initiative images are a common artifact produced by both Teleport's internal
and external facing codebases.


**1st party** - Teleport, and infrastructure we directly control such as our AWS accounts.

**2nd party** - Teleport's customers. These are the folks downloading Teleport artifacts.

**3rd party** - Vendors and upstream technology providers who offer artifact hosting.

**AWS** - Amazon Web Services - Teleport's primary cloud provider. We consider
AWS infrastructure inside of one of Teleport's AWS accounts to be 1st party.
Infrastructure provided by AWS itself (e.g. the public AMI registry) is considered
3rd party.

**Terraform** - A Infrastructure as Code tool developed by Hashicorp.  Of note:
Teleport provides a 1st party Terraform provider for users who wish to use
Terraform to configure a Teleport cluster.

**Mirroring/Mirrored** - Mirroring refers to copying an artifact from one
repository to another without changing any of the data or signatures on that
artifact.  In this design, artifacts hosted in 1st party repositories are
mirrored to 3rd party repositories.


### Scope

Mirroring external dependencies on our infrastructure important, but out of scope
for this RFD. Examples of out of scope external dependency mirrors include:

* A go module mirror
* A private NPM registry
* Mirroring unmodified external OCI images (e.g. alpine, or docker)


### How

During the promotion step defined in RFD 00XX: Artifact Promotion, the build
automation will promote the artifacts from the 1st party internal validation
repository to the 1st party sanctioned repo _and_ copy the artifact to any 3rd
party mirrors.


Mirrors must be clearly labeled as such.

#### Examples

As an example, consider the OCI images Teleport currently publishes at
`quay.io/gravitational/teleport`.  quay.io is infrastructure Teleport does not
control, and should thus be considered 3rd party.

Assumptions:
1) We want to host OCI images in a 1st party repository
2) We want to mirror OCI images to their historic quay.io location for years
   due to backwards compatibility reasons.
3) Additionally, we'd like to mirror OCI images to the offical Docker Hub.

With these in mind, lets look at the promote step

TAG     

push to



Mirroring to terraform 


### How do we choose which 3rd party repos we mirror to?

We consider the following factors:

1. User demand, as expressed in GitHub issues and customer feedback.
2. The fraction of installs that this would impact.
3. Security and stability of the vendor.  Consider elements such as:
  * Is the vendor smaller or larger than Teleport? Do they have a sustainable
    funding model?
  * Does the vendor have a track record of previous security issues?
  * If an attacker could inject arbitrary content into the 3rd party repo, would
    Teleport artifacts be the most valuable target, or one amongst many?  In the
    case of something like the Docker Hub, the value of editing teleport artifacts
    is 




### Security

Each additional place we choose to mirror artifacts adds ever-present
vulnerability: it is one more vendor and set of infrastructure that can be
compromised, potentially granting access to users teleport clusters and the
infrastructure those clusters secure. However, that risk is present whether
our end users are downloading images provided by Teleport or the most highly
rated community images. I believe it is better to provide a sensible default
than let the community ecosystem fill the void.

Each external repository will need its own set of credentials.  Many of these
have remedial multi-user support. Our automation will need a username/password
combination to publish artifacts.

Furthermore, mirroring will suffer limited auditability.  Each 3rd party platform
offers a different level of support for auditability, virtually all of them far behind
our standards defined in [Cloud RFD 0017: Artifact Storage Standards](https://github.com/gravitational/cloud/blob/4ab0965066687a4bd4a4f4a3e1ff072f45a69734/rfd/0017-artifact-storage-standards.md).

The downside of auditability is somewhat ameliorated because we will retain the
original artifacts (which should be bit-for-bit compareable) as well as our
internal publishing records.

Mirroring artifacts introduces the


* Explore possible attack vectors, explain how to prevent them
* If introducing new attack surfaces (UI, CLI commands, API or gRPC endpoints),
  consider how they may be abused and how to prevent it
* If introducing new auth{n,z}, explain their design and consequences
* If using crypto, show that best practices were used to define it


Reconcilliation

### UX

#### End User UX

The UX changes will depend on the tool.  For docker, using the mirror will be as
simple as:

```
docker run --rm -it gravitational/teleport:9.0.0
```

instead of the sanctioned:

```
docker run --rm -it quay.io/gravitational/teleport:9.0.0
```

However, because our documentation will never recommend the use of the mirrors,
we don't particularly need to worry about UX affordances.  Only users
specifically seeking out these repos outside of our sanctioned install will
encounter them.

We will want to mention mirrored Repos in the documentation, but in a similar
manner to [how we handle Homebrew](https://github.com/gravitational/teleport/blame/0a2d90f48910f9e4b3528be10f29ef1a898959a5/docs/pages/installation.mdx#L53-L57):

    The Teleport (image|provider|etc) in (Docker Hub|Hashicorp's Registry|etc) is
    published by Teleport but we do not maintain the infrastructure and can't
    guarantee its reliability or security. We recommend the use of our first party
    artifacts at (goteleport.com/downloads|releases.teleport.dev).

#### Release Engineer & Maintainer UX

Mirroring will only occur during the final promotion stage of the release
process. This makes it light weight and low risk, as the mirror step will
be an extra upload target after (or concurrent with) the artifacts being
promoted to the sanctioned 1st party repository.

One challenge with the promotion step is that is has been historically
difficult for release engineers to test changes to these steps.
Traditionally each 

To make testing promotion easier


## Related Work

This RFD is inspired by:

* Trent Clarke's work on publishing the Teleport Terraform Provider. See [teleport-plugins#444](https://github.com/gravitational/teleport-plugins/pull/444).
* Logan Davis' work on OCI image hosting.
* Our [artifact storage standards](https://github.com/gravitational/cloud/blob/2a4f4993f7715658f5130106511df7f22ed812a0/rfd/0017-artifact-storage-standards.md)
* Gus Luxton's seminal work on the Teleport build system.
