---
authors: Alexey Kontsevoy (alexey@goteleport.com)
state: draft
---

# RFD 38 - Teleport Terminal

## What
Teleport Terminal will be a fully featured terminal that provides easy access to remote resources via Teleport.
It can  be used as an alternative to existing terminals as well.

This RFD defines the high-level architecture for Teleport Terminal.

## Details
Teleport Terminal will consist of the following components (processes).
1. Terminal
2. Terminal Daemon
3. Terminal UI

### Terminal
Terminal will be an [Electron](https://www.electronjs.org/) based application. It will be responsible of initialization of Terminal Daemon and Terminal UI processes.
It will handle OS level resources such as native windows, local user information, dialogs, system trays, and other platform specific resources.

### Terminal Daemon
Terminal Daemon will be a system service written in golang that will be packaged with electron application. It will run under local user account and will have the following responsibilities:
1. Access Management to remote Teleport resources.
1. User profiles and certificate management.
2. Proxy connections to remote resources.
3. Client API.

#### Access Management to remote Teleport resources.
The service will be responsible of adding Teleport Clusters and providing access to remote resources.
It will work similarly to `tsh` thus it makes sense to add this functionality to `tsh` allowing it to start as `tshd` service.
The service will also manage local pty sessions.

#### User profiles and certificate management.
It will be possible to connect to many clusters and utilize their resources at the same time. The service will
manage all certificates by storing them in the user profile. It will utilize local files for storing user profiles.

#### Proxy connections to remote resources.
There will be two ways to create proxy connections: TCP on localhost and Unix Domain Sockets.

The first one will require creating a TCP connection on localhost with a random port. This will allow clients to establish connections using (https://127.0.0.1:XXX) addresses.
These connections will not be accessible from local networks by default but it should be possible to white-list IP addresses to allow incoming connections.

In both cases the service will perform SNI/ALPN routing to Teleport resources.

#### Client API
Service will expose REST API for Terminal UI. It will use Unix Sockets (similarly to docker daemon API) with file permissions. Unix Sockets are now broadly supported as Microsoft added Unix Sockets support to Windows (Windows 10 Version 1803).

On systems where Unix Sockets are not supported, it can use TLS/TCP where TLS certificates can be re-generated at start-time by the Teleport Terminal.


1. Adding a cluster. During this operation, the service will retrieve cluster auth preferences and store it in a user profile.

```
POST /cluster/

request {
  address: string;
}

response {
  name: string;
  address: string;
  status: 'disconnected',
  authPreferences: { }
}
```

2. Logging into cluster.

The service will provide APIs for different logins. Once logged in, the service will store the cluster certificate in the user profile. It will work similarly to `tsh`.

```
POST /cluster/:name/login/local
```

3. Once connected, below APIs will provide access to Teleport cluster resources.

```
GET  /clusters/:name/servers
GET  /clusters/:name/dbs
GET  /clusters/:name/apps
```

#### Creating proxy connections

Bellow request will create a local proxy connection to remote database.

```
POST  /gateways/

request {
  resourceURI: string; (clusters/:name/db/)
  proto: tcp | unix;
  tcp: {
    port?: number
  }
}

response {
  id: string;
  connectionString: string;
  resourceName: string;
  resourceKind: string;
}

```

To terminate a connection

```
DELETE /gateways/:id
```

To retrieve a list of existing connections

```
GET /gateways/
```

### Terminal UI
Terminal UI will be a web application running as Electron renderer process. It will be written in typescript and will utilize
nodejs platform to achieve desktop-like experience.
It will comminute with the deamon over REST API and will use named pipes to communicate with the Terminal (main) process.

It will have the following features:
1. Built-in fully featured terminal. It will use [xterm](https://xtermjs.org/).
2. Tabbed layout where a tab can be an ssh session, rdp connection, or any other document.
3. Support for adding/removing Teleport clusters.
4. Browsing of remote Teleport Resources.
5. Management of proxy connections.
6. Resource navigator (tree view)


### Diagram
```pro
                                                  +------------+
                                                  |            |
                                          +-------+---------+  |
                                          |                 |  |
                                          |    teleport     +--+
                                          |     clusters    |
                                          |                 |
                                          +------+-+--------+
                                                 ^ ^           External Network
+------------------------------------------------|-|---------------------+
                                                 | |           Host OS
           Clients (psql)                        | |
              |                                  | |
              v                                  | |
     +--------+---------------+                  | |
     |                        |        SNI/ALPN  | | GRPC
  +--+----------------------+ |         routing  | |
  |                         | |                  | |
  |     proxy connections   +-+                  | |
  |                         |                    | |
  +-------------------+-----+                    | |
                      ^                          | |
                      |                          | |
  +---------------+   | tls/tcp on localhost     | |
  |    local      |   |                          | |
  | user profile  |   |                          v v
  |   (files)     |   |                   +------+-+-------------------+
  +-------^-------+   |                   |                            |
          ^           +-------------------+      Terminal Daemon       |
          |                               |          (tshd)            |
          +<------------------------------+                            |
          |                               +-------------+--------------+
 +--------+-----------------+                           ^
 |         Terminal         |                           |
 |    Electron Main Process |                           |       rest API
 +-----------+--------------+                           |     (domain socket)
             ^                                          |
             |                                          |
    IPC      |                                          |
 named pipes |                                          |
             v  Terminal UI (Electron Renderer Process) |
 +-----------+------------+---------------------------------------------+
 | -gateways              | root@node1 × | k8s_c  × | rdp_win2 ×  |     |
 |   https://localhost:22 +---------------------------------------------+
 |   https://localhost:21 |                                             |
 +------------------------+ ./                                          |
 | -clusters              | ../                                         |
 |  -cluster1             | assets/                                     |
 |   +servers             | babel.config.js                             |
 |     node1              | build/                                      |
 |     node2              | src/                                        |
 |   -dbs                 | alexey@p14s:~/go/src/                       |
 |    mysql+prod          |                                             |
 |    mysql+test          |                                             |
 |  +cluster2             |                                             |
 |  +cluster3             |                                             |
 +------------------------+---------------------------------------------+
```