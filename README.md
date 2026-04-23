# Multiplexer

A Viam module that exposes a single generic service which fans out `DoCommand` and `Status` calls to a configured list of underlying generic services in parallel, then aggregates the per-dependency responses (and errors) into one map. Use it when a client should talk to a single logical endpoint that drives several backend generic services at once — for example, broadcasting a command to every backend, collecting status from a fleet of services, or composing a higher-level service from several existing ones without modifying them.

Per-dependency failures are logged as warnings and surfaced in the response, but do not cause the overall call to fail.

## Models

This module provides the following model(s):

- [`viam:multiplexer:generic-service-multiplexer`](#model-viammultiplexergeneric-service-multiplexer) — fans out `DoCommand` / `Status` to a configured list of generic service dependencies.

## Model: viam:multiplexer:generic-service-multiplexer

Wraps a list of generic services declared via `dependencies`. Every `DoCommand` and `Status` call is dispatched concurrently (one goroutine per dependency); once all dependencies have returned, the multiplexer aggregates the responses into:

```json
{
  "results": {
    "<dep_name>": <response_from_that_dep>
  },
  "errors": {
    "<dep_name>": "<error message>"
  }
}
```

Successful dependencies appear in `results`; failing ones appear in `errors`. The multiplexer itself never returns an error from `DoCommand` / `Status` — partial failures are surfaced through the `errors` map only.

The service uses `AlwaysRebuild`, so any change to `dependencies` triggers a full rebuild of the resource.

### Configuration

The following attribute template can be used to configure this model:

```json
{
  "dependencies": ["<generic_service_name>", "<generic_service_name>"]
}
```

#### Attributes

The following attributes are available for this model:

| Name           | Type     | Inclusion | Description                                                                                                    |
|----------------|----------|-----------|----------------------------------------------------------------------------------------------------------------|
| `dependencies` | []string | Required  | Names of the generic services to fan out to. Must contain at least one entry; entries cannot be empty strings. |

#### Example Configuration

```json
{
  "dependencies": [
    "primary_backend",
    "secondary_backend"
  ]
}
```

### DoCommand

The multiplexer forwards the entire `cmd` payload to each dependency unchanged and returns the aggregated `results` / `errors` shape described above. There are no commands handled directly by the multiplexer.

#### Example DoCommand

Sending the following command to a multiplexer configured with `primary_backend` and `secondary_backend`:

```json
{
  "ping": {}
}
```

returns (when both succeed):

```json
{
  "results": {
    "primary_backend": { "pong": true },
    "secondary_backend": { "pong": true }
  },
  "errors": {}
}
```

If `secondary_backend` fails, the call still succeeds and the failure is reported per-dependency:

```json
{
  "results": {
    "primary_backend": { "pong": true }
  },
  "errors": {
    "secondary_backend": "rpc error: ..."
  }
}
```
