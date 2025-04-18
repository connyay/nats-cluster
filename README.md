# Global NATS Cluster

[NATS](https://docs.nats.io/) is an open source messaging backend suited to many use cases and deployment scenarios. We use it for internal communications at Fly. This repo shows how to use it for your application.

This example creates a federated mesh of NATS servers that communicate over the private, encrypted IpV6 network available to all Fly organizations.

## Setup

1. `fly launch --no-deploy`
2. Edit fly.toml to use the `nats` image, configure a volume, and set up health checks:

```toml
[build]
  image = "jeffh/nats-cluster:main"

[mounts]
  source = "nats_cluster__data"
  destination = "/nats-store"
  initial_size = "10gb"
  auto_extend_size_threshold = 80
  auto_extend_size_increment = "10GB"
  auto_extend_size_limit = "100GB"

[checks]
  [checks.health]
    grace_period = "30s"
    interval = "15s"
    method = "get"
    path = "/healthz"
    port = 8222
    type = "http"
```

2. `fly deploy`

    > This will start NATS with a single node in your selected region. This should fail

3. `fly scale count 3`

    > This will scale the cluster to 3 nodes within the same region.

3. Add more regions with `flyctl scale count 3 --region sfc` or

Then run `flyctl logs` and you'll see the virtual machines discover each other.

```
2020-11-17T17:31:07.664Z d1152f01 ord [info] [493] 2020/11/17 17:31:07.646272 [INF] [fdaa:0:1:a7b:abc:21de:af5f:2]:4248 - rid:1 - Route connection created
2020-11-17T17:31:07.713Z 21deaf5f cdg [info] [553] 2020/11/17 17:31:07.704807 [INF] [fdaa:0:1:a7b:81:d115:2f01:2]:34902 - rid:19 - Route connection created
2020-11-17T17:31:08.123Z 82fabc30 syd [info] [553] 2020/11/17 17:31:08.114852 [INF] [fdaa:0:1:a7b:81:d115:2f01:2]:4248 - rid:7 - Route connection created
2020-11-17T17:31:08.259Z d1152f01 ord [info] [493] 2020/11/17 17:31:08.241644 [INF] [fdaa:0:1:a7b:b92:82fa:bc30:2]:45684 - rid:2 - Route connection created
```

