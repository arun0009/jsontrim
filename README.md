# jsontrim

[![CI](https://img.shields.io/github/actions/workflow/status/arun0009/jsontrim/ci.yml?branch=main&logo=github)](https://github.com/arun0009/jsontrim/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/arun0009/jsontrim)](https://goreportcard.com/report/github.com/arun0009/jsontrim)
[![PkgGoDev](https://pkg.go.dev/badge/github.com/arun0009/jsontrim)](https://pkg.go.dev/github.com/arun0009/jsontrim)

A lightweight, high-performance Go library for trimming JSON payloads to enforce size limits while preserving validity and data structure. Perfect for API responses, structured logging, and event recording where oversized data can cause outages.

Key features:
- **Smart Limits**: Drop or truncate fields > N bytes recursively.
- **Total Size Cap**: Iteratively remove elements until under the limit using size estimation to reduce GC pressure.
- **Wildcard Blacklisting**: Exclude sensitive paths dynamically (e.g., `users.*.password`).
- **Ghost Markers**: Optionally replace dropped fields with `"[TRIMMED]"` instead of deleting them, preserving schema visibility.
- **Order Preservation**: Safely trims arrays without destroying element order.
- **Strategies**: Choose removal order (largest-first, FIFO, prioritize keys).
- **Hooks**: Custom pre/post processing.
- Zero dependencies beyond `encoding/json`.

## Installation

```bash
go get github.com/arun0009/jsontrim
```
### Quick Start

```go
package main

import (
	"fmt"
    "strings"

	"github.com/arun0009/jsontrim"
)

func main() {
    // A large JSON payload with sensitive data
	raw := []byte(`{
        "id": "123", 
        "data": "` + strings.Repeat("x", 2000) + `", 
        "users": [{"id":1, "pass":"secret"}, {"id":2, "pass":"secret"}]
    }`)

	trimmer := jsontrim.New(jsontrim.Config{
		FieldLimit:        500,
		TotalLimit:        1024,
		Blacklist:         []string{"users.*.pass"}, // Wildcard support
		ReplaceWithMarker: true,                     // Leave a trace of what was removed
		Strategy:          jsontrim.RemoveLargest(""),
	})

	out, err := trimmer.Trim(raw)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Trimmed JSON (%d bytes): %s\n", len(out), out)
	// Output: {"id":"123","data":"[TRIMMED]","users":[{"id":1},{"id":2}]}
}
```

## Configuration

Pass a `Config` to `New()`:

- **FieldLimit** (`int`, default: 500): Max bytes per field/object/array (after nested trim).
- **TotalLimit** (`int`, default: 1024): Max total output bytes.
- **Blacklist** (`[]string`, default: `[]`): Dot-notation paths to exclude. Supports * wildcards.
- **ReplaceWithMarker** (bool, default: false): If true, removed fields/items are replaced with "[TRIMMED]" string value instead of being deleted. Useful for debugging.
- **Strategy** (`TruncStrategy`, default: `RemoveLargest`): Removal policy: `RemoveLargest("")`, `FIFO{}`, or `PrioritizeKeys`.
- **MaxDepth** (`int`, default: 10): Recursion depth to prevent stack overflows.
- **TruncateStrings** (`bool`, default: `false`): Append "..." to oversized strings instead of dropping.
- **Hooks** (`Hooks`, default: `{}`): `PreTrim`/`PostTrim` funcs for custom logic.

## Strategies

* `RemoveLargest("")`: Greedily drops the biggest fields/items to maximize retention (default).
* `FIFO{}`: Removes in iteration order (faster for ordered data).
* `PrioritizeKeys{KeepKeys: []string{"id", "ts"}, Fallback: &FIFO{}}`: Delays removal of key fields.

## Blacklisting & Wildcards

Uses dot-notation. Supports * as a wildcard for array indices or dynamic map keys.

* "user.password": Matches exact path.
* "users.*.password": Matches password inside any object in the users array (e.g., users[0].password, users[1].password).
* "logs.*": Matches everything inside logs.

## Hooks Example

```go
import (
	"log"
	"time"  
)

Hooks{
    PreTrim: func(v interface{}) interface{} {
        if m, ok := v.(map[string]interface{}); ok {
            m["trimmed_at"] = time.Now().Format(time.RFC3339)
        }
        return v
    },
    PostTrim: func(v interface{}, err error) interface{} {
        if err != nil {
            log.Printf("Trim error: %v", err)
        }
        return v
    },
}
```

## Use Cases
* **Structured Logging**: Prevent large fields (like massive stack traces, base64 images, or entire HTTP bodies) from **crashing log aggregators** (ELK, Splunk) or consuming excessive bandwidth. jsontrim acts as a safety valve in log hooks.
* **API Middleware**: Ensure **API responses** strictly adhere to size contracts, preventing issues in client-side applications or with platform limits (e.g., Lambda/API Gateway payload size caps).
* **Audit and Compiance**: Use the **Blacklist** (especially with wildcards) to sanitize Personally Identifiable Information (**PII**) or sensitive credentials before persisting the data to an audit log or database.
* **Event Stream Processing**: Guarantee that messages pushed to queues (Kafka, RabbitMQ, SQS) never exceed topic size limits, avoiding message rejection and processing failures.

## License

MIT License. See LICENSE.