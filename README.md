# Go HTTP Client-Side Load Balancing

[![Build](https://github.com/bufbuild/httplb/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/bufbuild/httplb/actions/workflows/ci.yaml)
[![Report Card](https://goreportcard.com/badge/github.com/bufbuild/httplb)](https://goreportcard.com/report/github.com/bufbuild/httplb)
[![GoDoc](https://pkg.go.dev/badge/github.com/bufbuild/httplb.svg)](https://pkg.go.dev/github.com/bufbuild/httplb)

[`httlb`](https://pkg.go.dev/github.com/bufbuild/httplb)
provides client-side load balancing for `net/http` clients. By default,
clients are designed for server-to-server and RPC workloads:

* They support HTTP/1.1, HTTP/2, and h2c.
* They periodically re-resolve names using DNS.
* They use a round-robin load balancing policy.

Random, fewest-pending, and power-of-two load balancing policies are also available.
Clients with more complex needs can customize the underlying transports, name
resolution, and load balancing. They can also add subsetting and active health
checking.

`httplb` takes care to build all this functionality underneath the standard library's
`*http.Client`, so `httplb` is usable anywhere you're currently using `net/http`.

## Status: Alpha

This project is currently in **alpha**. The API should be considered unstable and likely to change.

## Legal

Offered under the [Apache 2 license][badges_license].

[badges_license]: https://github.com/bufbuild/knit-go/blob/main/LICENSE
