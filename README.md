# NATS-GW - Simple HTTP => NATS gateway

A simple HTTP gateway for [NATS](https://github.com/nats-io). Useful for webhooks, listens for POSTs at /topics/{topic} and /requests/{topic}, and sends the request body to the specified topic.

- Messages sent to /topics/{topic} do not expect a response. The webhook finishes as soon as the message is pushed to the NATS server.
- Messages sent to /requests/{topic} do expect a response. The webhook return the response body as is.

Currently, it only supports TLS nats servers with a valid public key, and username/pass authentication.

## Usage

Start one instance in server mode:

```bash
nats-gw -user <username> -pass <password> -host <server IP> -port <server port>
```

Start another instance in test mode, listening for some topic:

```bash
nats-gw -user <username> -pass <password> -host <server IP> -port <server port> -test my_topic
```

Send a message to the topic:

```bash
curl -X POST -H "Content-Type: application/json" http://localhost:8080/topics/my_topic -d '{"p1": "v1", "p2": "v2" }'
```

Or send a request:

```bash
curl -X POST -H "Content-Type: application/json" http://localhost:8080/requests/my_topic -d '{"p1": "v1", "p2": "v2" }'
```
