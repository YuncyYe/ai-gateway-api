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

package iintent_config

import (
	"context"

	"github.com/rainway-ai-gateway/ai-gateway-api/model/ioperlog"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/itxn"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/iversion_control"
)

type fakeTxn struct{}

func (f *fakeTxn) AtomExecute(ctx context.Context, do func(context.Context) error) error {
	return do(ctx)
}

var _ itxn.TxnStorager = (*fakeTxn)(nil)

// fakeIntentConfigStorager emulates the singleton DB behavior: Upsert
// overwrites the only row (fixed id=1), Fetch reads it back.
type fakeIntentConfigStorager struct {
	fetchFn  func(ctx context.Context) (*IntentConfig, error)
	upsertFn func(ctx context.Context, config *IntentConfig) error

	config *IntentConfig

	fetchCalls  int
	upsertCalls []*IntentConfig
}

func (s *fakeIntentConfigStorager) Fetch(ctx context.Context) (*IntentConfig, error) {
	s.fetchCalls++
	if s.fetchFn != nil {
		return s.fetchFn(ctx)
	}
	return s.config, nil
}

func (s *fakeIntentConfigStorager) Upsert(ctx context.Context, config *IntentConfig) error {
	s.upsertCalls = append(s.upsertCalls, config)
	if s.upsertFn != nil {
		return s.upsertFn(ctx, config)
	}
	s.config = config
	return nil
}

var _ IntentConfigStorager = (*fakeIntentConfigStorager)(nil)

type fakeVersionControlStorager struct {
	upsertFn func(ctx context.Context, css *iversion_control.ExportData) (string, error)

	seen []*iversion_control.ExportData
}

func (s *fakeVersionControlStorager) UpsertConfigLastExportedVersion(ctx context.Context, css *iversion_control.ExportData) (string, error) {
	s.seen = append(s.seen, css)
	if s.upsertFn != nil {
		return s.upsertFn(ctx, css)
	}
	return "", nil
}

var _ iversion_control.VersionControlStorager = (*fakeVersionControlStorager)(nil)

type fakeOperationLogRecorder struct {
	entries []*ioperlog.OperationLogEntry
}

func (f *fakeOperationLogRecorder) Record(ctx context.Context, entry *ioperlog.OperationLogEntry) {
	f.entries = append(f.entries, entry)
}

var _ ioperlog.OperationLogRecorder = (*fakeOperationLogRecorder)(nil)
