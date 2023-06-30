// Copyright 2023 Buf Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package httplb

import (
	"context"

	"github.com/bufbuild/httplb/conn"
	"github.com/bufbuild/httplb/health"
	"github.com/bufbuild/httplb/internal"
	"github.com/bufbuild/httplb/picker"
	"github.com/bufbuild/httplb/resolver"
)

type Balancer = balancer

type ConnManager = connManager

func NewBalancer(
	ctx context.Context,
	picker picker.Factory,
	checker health.Checker,
	pool connPool,
) *Balancer { //nolint:revive // exported alias not handled by linter
	return newBalancer(ctx, picker, checker, pool)
}

func (b *Balancer) Start() {
	b.start()
}

func (b *Balancer) SetUpdateHook(updateHook func([]resolver.Address, []conn.Conn)) {
	b.updateHook = updateHook
}

func (b *Balancer) SetClock(clock internal.Clock) {
	b.clock = clock
}

func (c *ConnManager) ReconcileAddresses(addrs []resolver.Address, updateFunc func([]resolver.Address, []conn.Conn) []conn.Conn) {
	c.reconcileAddresses(addrs, updateFunc)
}
