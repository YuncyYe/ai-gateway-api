// Copyright(c) 2026 The Rainway AI Gateway (壬远AI网关) Authors.
//
//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//http://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License. All rights reserved.

// Copyright (c) 2021 The BFE Authors.
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

// Package xmask provides display masking for secret-like strings (API keys,
// provider keys, tokens). Masked values keep a few head/tail characters so
// operators can identify ownership, never expose the original value, and must
// not be used for authentication.
package xmask

// Secret masks a secret-like string for display:
//   - len > 8:   first 4 + "********" + last 4
//   - 4 < len <= 8: first 2 + "****" + last 2
//   - len <= 4:  "********"
func Secret(s string) string {
	n := len(s)
	switch {
	case n <= 4:
		return "********"
	case n <= 8:
		return s[:2] + "****" + s[n-2:]
	default:
		return s[:4] + "********" + s[n-4:]
	}
}
