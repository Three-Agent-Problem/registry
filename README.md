# registry

An agent-card linter and a small registry for [A2A](https://a2a-protocol.org) agents.

Few agent cards on the open web are usable by an A2A 1.0 client. [A July 2026 probe](https://apievangelist.com/2026/07/29/most-published-agent-cards-are-not-actually-a2a/) found 65 cards on 22,341 hosts, and most of those failed basic checks: `capabilities` sent as an array, 0.x fields, no `supportedInterfaces`, a legacy path. `card-lint` catches these problems before a client trips on them, and the registry only lists agents whose cards pass.

| Path | What it does |
|---|---|
| [`lint`](lint/lint.go) | `Check(raw) Report` grades a card as `conformant`, `near-conformant` or `non-conformant`, with findings. It checks the **raw JSON**, so it catches what a typed decode would hide. `Fetch` gets a card the way clients do: `/.well-known/agent-card.json`, falling back to the legacy `/.well-known/agent.json` (which counts as an error). |
| [`cmd/card-lint`](cmd/card-lint/main.go) | CLI. Exits 1 on errors (`-strict` also fails on warnings), and `-json` prints the report as JSON. Use it in CI before you deploy an agent. |
| [`server`](server/server.go) | The registry. Agents register by URL, and the registry fetches and lints their card, then re-checks liveness on an interval. |
| [`client`](client/client.go) | Go client: `Register`, `Search`. |
| [`cmd/registry`](cmd/registry/main.go) | The registry as a server, listening on `:7070` by default. |

## card-lint

```sh
go run ./cmd/card-lint https://agent.example.com     # fetches the well-known card
go run ./cmd/card-lint ./agent-card.json             # local file
```

```text
legacy-card.json: non-conformant
  error   supportedInterfaces   supportedInterfaces is required (a 0.x card declares url + preferredTransport instead; 1.0 clients look here)
  warning preferredTransport    preferredTransport is a 0.x field that 1.0 clients ignore; use supportedInterfaces[].protocolBinding
  warning protocolVersion       protocolVersion is a 0.x field that 1.0 clients ignore; use supportedInterfaces[].protocolVersion
  warning skills[0].tags        tags is required by the spec and is what registries search on
  warning url                   url is a 0.x field that 1.0 clients ignore; use supportedInterfaces[].url
```

Errors are what break a 1.0 client: required fields from the normative `a2a.proto`, wrong JSON kinds, relative or non-HTTP URLs. Warnings are fields 1.0 ignores, plain `http` outside localhost, non-standard bindings, and missing modes or tags.

## Registry API

| Request | Result |
|---|---|
| `POST /agents` `{"url": "https://agent.example.com"}` | `201` with the entry, or `422` with the lint report if the card has errors |
| `GET /agents?skill=quote` | Agents with a skill whose ID or tag is `quote` |
| `GET /agents?q=north` | Text search over name, description and skills |
| `GET /agents?all=1` | Also non-conformant and unreachable agents |
| `GET /api/check?url=…` | Lint without registering |
| `GET /`, `GET /check` | HTML listing and a check form |

Results are ranked by grade, then by latency in 100 ms bands, then by name.

The registry is an **index, not the source of truth**. Clients should fetch the card from the agent itself before calling it (the example buyer agent does this), and later verify its JWS signature.

## Roadmap

- Verified domains (signed card plus JWKS), search by input/output mode and extension, opt-in traffic reputation.
- A `card-lint` GitHub Action.
- A public registry, with self-hosted private registries staying open source.

## Development

```sh
go vet ./... && go test -race ./...
```

## License

Apache-2.0
