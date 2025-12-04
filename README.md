# IP checking

Testing upstream IPs to ensure they're safe.

## Features

- **Upstream IP Address Extraction**: The external processor can access the IP address of the upstream target when configured as an upstream HTTP filter. This is done through Envoy's request attributes system.

## Build Local

    make local.build

## Run Local

1) The target is webhook.site, get a GUID from there first.

2) In separate terminals launch two instances of the ext-proc service

    ./bin/extprocdemo --port 10000


2) Use func-e to launch Envoy (https://func-e.io/)

    func-e run -c config/envoy.yaml
or
    docker run --rm --net host --name envoy-transcode -v "$(pwd)/config:/config" envoyproxy/envoy:v1.36.2 envoy -c /config/envoy.yaml

3) Envoy is listening on port 8080, call that with the webhook.site GUID, e.g.

    curl http://localhost:8080/03e9d944-0431-4027-b969-7022fc34e576

