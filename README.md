# httplb

[![Build](https://github.com/bufbuild/httplb/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/bufbuild/httplb/actions/workflows/ci.yaml)
[![Report Card](https://goreportcard.com/badge/github.com/bufbuild/httplb)](https://goreportcard.com/report/github.com/bufbuild/httplb)
[![GoDoc](https://pkg.go.dev/badge/github.com/bufbuild/httplb.svg)](https://pkg.go.dev/github.com/bufbuild/httplb)

[`httplb`](https://pkg.go.dev/github.com/bufbuild/httplb)
provides client-side load balancing for `net/http` clients. By default,
clients are designed for server-to-server and RPC workloads:

* They support HTTP/1.1, HTTP/2, and [H2C](https://en.wikipedia.org/wiki/HTTP/2#Encryption).
* They periodically re-resolve names using DNS.
* They use a round-robin load balancing policy.

Random, fewest-pending, and power-of-two load balancing policies are also available.
Clients with more complex needs can customize the underlying transports, name
resolution, and load balancing. They can also add subsetting and active health
checking.

`httplb` takes care to build all this functionality underneath the standard library's
`*http.Client`, so `httplb` is usable anywhere you're currently using `net/http`.

## Example

Here's a quick example of how to get started with `httplb`:

```go
package main

import (
	"log"
	"net"
	"time"

	"github.com/bufbuild/httplb"
	"github.com/bufbuild/httplb/picker"
)

func main() {
	client := httplb.NewClient(
		// Switch from the default round-robin policy to power-of-two.
		httplb.WithPicker(picker.NewPowerOfTwo),
	)
	defer client.Close()
	resp, err := client.Get("https://example.com")
	if err != nil {
		log.Fatalln(err)
	}
	defer resp.Body.Close()
	log.Println(resp.Status)
}
```

If you're using [Connect](https://github.com/connectrpc/connect-go), you can
use `httplb` for your RPC clients:

```go
func main() {
	client := httplb.NewClient()
	defer client.Close()
	pingClient := pingv1connect.NewPingServiceClient(
		client,
		"http://localhost:8080/",
	)
	req := connect.NewRequest(&pingv1.PingRequest{
		Number: 42,
	})
	res, err := pingClient.Ping(context.Background(), req)
	if err != nil {
		log.Fatalln(err)
	}
	log.Println(res.Msg)
}
```

If you know the server supports HTTP/2 without TLS (HTTP/2 over cleartext, or H2C for short), use
the `h2c` scheme in your URLs instead of `http`:

```go
	pingClient := pingv1connect.NewPingServiceClient(
		client,
		"h2c://localhost:8080/",
	)
```

For more information on how to use `httplb`, especially for advanced use cases, take
a look at the [full documentation](https://pkg.go.dev/github.com/bufbuild/httplb).

## Ecosystem

* [connect-go](https://github.com/bufbuild/connect-go): RPC library, compatible with `httplb`.

## Status: Alpha

This project is currently in **alpha**. The API should be considered unstable and likely to change.

## Legal

Offered under the [Apache 2 license](LICENSE).

