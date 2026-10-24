// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package middleware

import (
	"context"
	"testing"

	"github.com/bfenetworks/go-lib/log"
	"github.com/bfenetworks/go-lib/log/log4go"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/itxn"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
)

// fakeTxn is a no-op transaction storager used by unit tests that need to
// build real manager instances.
type fakeTxn struct{}

func (f *fakeTxn) AtomExecute(ctx context.Context, do func(context.Context) error) error {
	return do(ctx)
}

var _ itxn.TxnStorager = (*fakeTxn)(nil)

// setupTestLoggers initializes global loggers with console output so that
// middleware which depends on them (recovery, access logger) can run in tests
// without writing to real log files. The original loggers are restored in the
// cleanup callback.
func setupTestLoggers(t *testing.T) {
	t.Helper()

	origAccess := stateful.AccessLogger
	origException := stateful.ExceptionLogger
	origGlobal := log.Logger

	stateful.AccessLogger = log4go.NewDefaultLogger(log4go.DEBUG)
	stateful.ExceptionLogger = log4go.NewDefaultLogger(log4go.DEBUG)
	log.Logger = stateful.AccessLogger

	t.Cleanup(func() {
		if stateful.AccessLogger != nil {
			stateful.AccessLogger.Close()
		}
		if stateful.ExceptionLogger != nil {
			stateful.ExceptionLogger.Close()
		}
		stateful.AccessLogger = origAccess
		stateful.ExceptionLogger = origException
		log.Logger = origGlobal
	})
}
