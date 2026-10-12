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

package xmask

import "testing"

func TestSecret(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "********"},
		{"one", "a", "********"},
		{"four", "abcd", "********"},
		{"five", "abcde", "ab****de"},
		{"eight", "abcdefgh", "ab****gh"},
		{"nine", "abcdefghi", "abcd********fghi"},
		{"api key", "ak-2v8x9k3m7p", "ak-2********3m7p"},
		{"provider key", "sk-aaaaaaaaaaaa", "sk-a********aaaa"},
		{"token", "Px2szn6R1HQo-WRSIJyt", "Px2s********IJyt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Secret(c.in); got != c.want {
				t.Errorf("Secret(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}

	// Non-ASCII input is truncated by byte (callers constrain keys/tokens to
	// ASCII alphabets); it must not panic and keeps the fixed 4+8+4 shape.
	t.Run("multibyte no panic", func(t *testing.T) {
		got := Secret("密钥密钥密钥密钥密钥")
		if len(got) != 16 {
			t.Errorf("Secret(multibyte) length = %d, want 16", len(got))
		}
	})
}
