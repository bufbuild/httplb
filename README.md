# Go HTTP Balancer

[![Build](https://github.com/bufbuild/go-http-balancer/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/bufbuild/go-http-balancer/actions/workflows/ci.yaml)
[![Report Card](https://goreportcard.com/badge/github.com/bufbuild/go-http-balancer)](https://goreportcard.com/report/github.com/bufbuild/go-http-balancer)
[![GoDoc](https://pkg.go.dev/badge/github.com/bufbuild/go-http-balancer.svg)](https://pkg.go.dev/github.com/bufbuild/go-http-balancer)

Go HTTP Balancer is a library that provides client-side load balancing
functionality on top of Go's built-in `net/http` client.

The main entry-point is the [`httlb`](https://pkg.go.dev/github.com/bufbuild/go-http-balancer/httplb)
package, which provides functions for creating a `Client`. There are numerous options for
controlling the behavior of the client. The options allow you to customize how the
underlying transports are configured as well as how the client resolves names into
addresses and performs load balancing.

The other packages provide supporting constructs and abstractions for customizable
name resolution and load balancing, in the [`resolver`](https://pkg.go.dev/github.com/bufbuild/go-http-balancer/resolver)
and [`balancer`](https://pkg.go.dev/github.com/bufbuild/go-http-balancer/balancer)
sub-packages respectively.

## Status: Alpha

This project is currently in **alpha**. The API should be considered unstable and likely to change.

## Legal

Offered under the [Apache 2 license][badges_license].

[badges_license]: https://github.com/bufbuild/knit-go/blob/main/LICENSE
