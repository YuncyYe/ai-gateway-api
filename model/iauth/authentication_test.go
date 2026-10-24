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

package iauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rainway-ai-gateway/ai-gateway-api/lib"
	"github.com/rainway-ai-gateway/ai-gateway-api/lib/xcrypto"
	"github.com/rainway-ai-gateway/ai-gateway-api/model/ibasic"
	"github.com/rainway-ai-gateway/ai-gateway-api/stateful"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupConfig(t *testing.T) {
	t.Helper()
	orig := stateful.DefaultConfig
	stateful.DefaultConfig = &stateful.Config{
		RunTime: stateful.RunTimeConfig{
			SessionExpireInDay: 1,
			SkipTokenValidate:  false,
		},
	}
	t.Cleanup(func() {
		stateful.DefaultConfig = orig
	})
}

func TestVisitor_Methods(t *testing.T) {
	t.Run("user visitor", func(t *testing.T) {
		v := &Visitor{User: &User{Name: "alice", Type: UserTypeNormal, Admin: true}}
		assert.Equal(t, "alice", v.GetName())
		assert.Equal(t, []string{ScopeSystem}, v.GetScopes())
		assert.Equal(t, UserTypeNormal, v.GetType())
		assert.True(t, v.IsAdmin())
	})

	t.Run("token visitor", func(t *testing.T) {
		v := &Visitor{Token: &Token{Name: "token-1", Scope: ScopeProduct}}
		assert.Equal(t, "token-1", v.GetName())
		assert.Equal(t, []string{ScopeProduct}, v.GetScopes())
		assert.Equal(t, UserTypeToken, v.GetType())
		assert.False(t, v.IsAdmin())
	})
}

func TestUser_Methods(t *testing.T) {
	t.Run("admin user", func(t *testing.T) {
		u := &User{Name: "admin", Admin: true}
		assert.Equal(t, []string{ScopeSystem}, u.GetScopes())
		assert.True(t, u.IsAdmin())
	})

	t.Run("normal user", func(t *testing.T) {
		u := &User{Name: "user", Admin: false}
		assert.Equal(t, []string{ScopeProduct}, u.GetScopes())
		assert.False(t, u.IsAdmin())
	})
}

func TestToken_Methods(t *testing.T) {
	t.Run("system token", func(t *testing.T) {
		tok := &Token{Name: "sys-token", Scope: ScopeSystem}
		assert.True(t, tok.IsAdmin())
	})

	t.Run("product token", func(t *testing.T) {
		tok := &Token{Name: "prod-token", Scope: ScopeProduct}
		assert.False(t, tok.IsAdmin())
	})
}

func TestMustGetVisitor(t *testing.T) {
	t.Run("from context", func(t *testing.T) {
		v := &Visitor{User: &User{Name: "alice"}}
		ctx := NewVisitorContext(context.Background(), v)
		got, err := MustGetVisitor(ctx)
		require.NoError(t, err)
		assert.Equal(t, "alice", got.GetName())
	})

	t.Run("skip token validate", func(t *testing.T) {
		setupConfig(t)
		stateful.DefaultConfig.RunTime.SkipTokenValidate = true
		ctx := context.Background()
		got, err := MustGetVisitor(ctx)
		require.NoError(t, err)
		assert.Equal(t, "SkipUser", got.GetName())
	})

	t.Run("not login", func(t *testing.T) {
		setupConfig(t)
		ctx := context.Background()
		_, err := MustGetVisitor(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "User Not Login")
	})
}

func TestAuthenticateManager_Authenticate_Password(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)

	t.Run("success", func(t *testing.T) {
		userName := "alice"
		password := "secret123"
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				if param.Name != nil && *param.Name == userName {
					return &User{Name: userName, Password: password}, nil
				}
				return nil, nil
			},
			updateUserFn: func(ctx context.Context, user *User, param *UserParam) error {
				return nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		v, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypePassword,
			Identify: userName,
			Extend:   password,
		})
		require.NoError(t, err)
		require.NotNil(t, v)
		assert.Equal(t, userName, v.GetName())
	})

	t.Run("user not exist", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return nil, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		_, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypePassword,
			Identify: "alice",
			Extend:   "secret123",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "User alice Not Exist")
	})

	t.Run("wrong password", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return &User{Name: "alice", Password: "secret123"}, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		_, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypePassword,
			Identify: "alice",
			Extend:   "wrong",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Password Wrong")
	})

	t.Run("bcrypt stored password", func(t *testing.T) {
		require.NoError(t, xcrypto.SetPasswordHashCost(4))
		t.Cleanup(func() {
			require.NoError(t, xcrypto.SetPasswordHashCost(xcrypto.DefaultPasswordHashCost))
		})
		userName := "alice"
		hashed, err := xcrypto.HashPassword("secret123")
		require.NoError(t, err)
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				if param.SessionKey != nil {
					return nil, nil // session-key collision check: never taken
				}
				return &User{Name: userName, Password: hashed}, nil
			},
			updateUserFn: func(ctx context.Context, user *User, param *UserParam) error {
				assert.Nil(t, param.Password, "hashed row must not be re-hashed")
				return nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		v, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypePassword,
			Identify: userName,
			Extend:   "secret123",
		})
		require.NoError(t, err)
		require.NotNil(t, v)
		assert.Equal(t, userName, v.GetName())
	})

	t.Run("legacy plaintext migrates on login", func(t *testing.T) {
		require.NoError(t, xcrypto.SetPasswordHashCost(4))
		t.Cleanup(func() {
			require.NoError(t, xcrypto.SetPasswordHashCost(xcrypto.DefaultPasswordHashCost))
		})
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				if param.SessionKey != nil {
					return nil, nil // session-key collision check: never taken
				}
				return &User{Name: "alice", Password: "secret123"}, nil
			},
			updateUserFn: func(ctx context.Context, user *User, param *UserParam) error {
				require.NotNil(t, param.Password)
				assert.True(t, xcrypto.IsHashedPassword(*param.Password),
					"legacy plaintext must be re-hashed on login, got %q", *param.Password)
				assert.NotNil(t, param.SessionKey)
				return nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		v, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypePassword,
			Identify: "alice",
			Extend:   "secret123",
		})
		require.NoError(t, err)
		require.NotNil(t, v)
		assert.True(t, xcrypto.IsHashedPassword(v.User.Password))
	})

	t.Run("SKIP literal no longer bypasses", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return &User{Name: "alice", Password: "realpass"}, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		_, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypePassword,
			Identify: "alice",
			Extend:   "SKIP",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Password Wrong")
	})

}

func TestAuthenticateManager_Authenticate_Session(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)

	t.Run("success", func(t *testing.T) {
		sessionKey := "valid-session"
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				if param.SessionKey != nil && *param.SessionKey == sessionKey {
					return &User{Name: "alice", SessionKey: sessionKey, SessionKeyCreateAt: time.Now()}, nil
				}
				return nil, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		v, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypeSessionKey,
			Identify: sessionKey,
		})
		require.NoError(t, err)
		require.NotNil(t, v)
		assert.Equal(t, "alice", v.GetName())
	})

	t.Run("session expired", func(t *testing.T) {
		sessionKey := "expired-session"
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return &User{
					Name:               "alice",
					SessionKey:         sessionKey,
					SessionKeyCreateAt: time.Now().AddDate(0, 0, -2),
				}, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		_, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypeSessionKey,
			Identify: sessionKey,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Session Key Expired")
	})

	t.Run("session wrong", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return nil, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		_, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypeSessionKey,
			Identify: "wrong-session",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Session Key Wrong")
	})
}

func TestAuthenticateManager_Authenticate_Token(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)

	t.Run("success", func(t *testing.T) {
		tokenVal := "valid-token"
		store := &fakeAuthenticateStorager{
			fetchTokensFn: func(ctx context.Context, param *TokenFilter) ([]*Token, error) {
				if param.Token != nil && *param.Token == tokenVal {
					return []*Token{{Name: "token-1", Token: tokenVal, Scope: ScopeProduct}}, nil
				}
				return nil, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		v, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypeToken,
			Identify: tokenVal,
		})
		require.NoError(t, err)
		require.NotNil(t, v)
		assert.Equal(t, "token-1", v.GetName())
	})

	t.Run("token wrong", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchTokensFn: func(ctx context.Context, param *TokenFilter) ([]*Token, error) {
				return nil, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		_, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypeToken,
			Identify: "wrong-token",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Token Wrong")
	})
}

func TestAuthenticateManager_Authenticate_Skip(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)

	t.Run("success when skip enabled", func(t *testing.T) {
		stateful.DefaultConfig.RunTime.SkipTokenValidate = true
		m := NewAuthenticateManager(&fakeTxn{}, &fakeAuthenticateStorager{}, &fakeAuthorizeStorager{})

		v, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypeSkip,
			Identify: ScopeSystem,
		})
		require.NoError(t, err)
		require.NotNil(t, v)
		assert.Equal(t, ScopeSystem, v.GetScopes()[0])
	})

	t.Run("fail when skip disabled", func(t *testing.T) {
		stateful.DefaultConfig.RunTime.SkipTokenValidate = false
		m := NewAuthenticateManager(&fakeTxn{}, &fakeAuthenticateStorager{}, &fakeAuthorizeStorager{})

		_, err := m.Authenticate(ctx, &AuthenticateParam{
			Type:     AuthTypeSkip,
			Identify: ScopeSystem,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Bad Authorization Flag")
	})
}

func TestAuthenticateManager_Authenticate_UnknownType(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)
	m := NewAuthenticateManager(&fakeTxn{}, &fakeAuthenticateStorager{}, &fakeAuthorizeStorager{})

	_, err := m.Authenticate(ctx, &AuthenticateParam{Type: "Unknown"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Illegal Authenticate Type")
}

func TestAuthenticateManager_DestroySessionKey(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)

	t.Run("success", func(t *testing.T) {
		sessionKey := "session-1"
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				if param.SessionKey != nil && *param.SessionKey == sessionKey {
					return &User{Name: "alice", SessionKey: sessionKey}, nil
				}
				return nil, nil
			},
			updateUserFn: func(ctx context.Context, user *User, param *UserParam) error {
				return nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		require.NoError(t, m.DestroySessionKey(ctx, sessionKey))
	})

	t.Run("session not exist", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return nil, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		err := m.DestroySessionKey(ctx, "not-exist")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Session Key Not Exist")
	})
}

func TestAuthenticateManager_CreateUser(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)

	t.Run("success", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return nil, nil
			},
			createUserFn: func(ctx context.Context, param *UserParam) error {
				require.NotNil(t, param.Password)
				assert.True(t, xcrypto.IsHashedPassword(*param.Password),
					"storager must receive the hashed password, got %q", *param.Password)
				return nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		password := "secret123"
		err := m.CreateUser(ctx, &UserParam{
			Name:     lib.PString("alice"),
			Password: &password,
		})
		require.NoError(t, err)
	})

	t.Run("password too short", func(t *testing.T) {
		m := NewAuthenticateManager(&fakeTxn{}, &fakeAuthenticateStorager{}, &fakeAuthorizeStorager{})
		password := "123"
		err := m.CreateUser(ctx, &UserParam{Password: &password})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Password Length Must Be Between 8 and 72 Bytes")
	})

	t.Run("user existed", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return &User{Name: "alice"}, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		password := "secret123"
		err := m.CreateUser(ctx, &UserParam{
			Name:     lib.PString("alice"),
			Password: &password,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "User Record Existed")
	})
}

func TestAuthenticateManager_DeleteUser(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)

	t.Run("success", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return &User{Name: "alice"}, nil
			},
			deleteUserFn: func(ctx context.Context, user *User) error {
				return nil
			},
		}
		authStore := &fakeAuthorizeStorager{
			unbindUserAllProductFn: func(ctx context.Context, user *User) error {
				return nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, authStore)

		require.NoError(t, m.DeleteUser(ctx, "alice"))
	})

	t.Run("user not exist", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return nil, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		err := m.DeleteUser(ctx, "alice")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "User Record Not Exist")
	})
}

func TestAuthenticateManager_UpdateUserPassword(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)

	t.Run("success with legacy plaintext old password", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return &User{Name: "alice", Password: "oldpass"}, nil
			},
			updateUserFn: func(ctx context.Context, user *User, param *UserParam) error {
				require.NotNil(t, param.Password)
				assert.True(t, xcrypto.IsHashedPassword(*param.Password),
					"new password must be stored hashed, got %q", *param.Password)
				return nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		err := m.UpdateUserPassword(ctx, &PasswordChangeData{
			UserName:    "alice",
			OldPassword: "oldpass",
			Password:    "newpass123",
		})
		require.NoError(t, err)
	})

	t.Run("success with bcrypt old password", func(t *testing.T) {
		require.NoError(t, xcrypto.SetPasswordHashCost(4))
		t.Cleanup(func() {
			require.NoError(t, xcrypto.SetPasswordHashCost(xcrypto.DefaultPasswordHashCost))
		})
		hashedOld, err := xcrypto.HashPassword("oldpass")
		require.NoError(t, err)
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return &User{Name: "alice", Password: hashedOld}, nil
			},
			updateUserFn: func(ctx context.Context, user *User, param *UserParam) error {
				require.NotNil(t, param.Password)
				assert.True(t, xcrypto.IsHashedPassword(*param.Password),
					"new password must be stored hashed, got %q", *param.Password)
				return nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		err = m.UpdateUserPassword(ctx, &PasswordChangeData{
			UserName:    "alice",
			OldPassword: "oldpass",
			Password:    "newpass123",
		})
		require.NoError(t, err)
	})

	t.Run("wrong old password", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserFn: func(ctx context.Context, param *UserFilter) (*User, error) {
				return &User{Name: "alice", Password: "oldpass"}, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		err := m.UpdateUserPassword(ctx, &PasswordChangeData{
			UserName:    "alice",
			OldPassword: "wrong",
			Password:    "newpass123",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Invalid Password")
	})

	t.Run("password too short", func(t *testing.T) {
		m := NewAuthenticateManager(&fakeTxn{}, &fakeAuthenticateStorager{}, &fakeAuthorizeStorager{})

		err := m.UpdateUserPassword(ctx, &PasswordChangeData{
			UserName: "alice",
			Password: "123",
		})
		require.Error(t, err)
	})
}

func TestAuthenticateManager_FetchUser(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)

	t.Run("found", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserListFn: func(ctx context.Context, param *UserFilter) ([]*User, error) {
				return []*User{{Name: "alice"}}, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		user, err := m.FetchUser(ctx, &UserFilter{Name: lib.PString("alice")})
		require.NoError(t, err)
		require.NotNil(t, user)
		assert.Equal(t, "alice", user.Name)
	})

	t.Run("not found", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchUserListFn: func(ctx context.Context, param *UserFilter) ([]*User, error) {
				return nil, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		user, err := m.FetchUser(ctx, &UserFilter{Name: lib.PString("alice")})
		require.NoError(t, err)
		assert.Nil(t, user)
	})
}

func TestAuthenticateManager_FetchTokens(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)

	t.Run("success with products", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchTokensFn: func(ctx context.Context, param *TokenFilter) ([]*Token, error) {
				return []*Token{{ID: 1, Name: "token-1"}}, nil
			},
		}
		authStore := &fakeAuthorizeStorager{
			batchFetchTokenProductFn: func(ctx context.Context, token []*Token) (map[int64]*ibasic.Product, error) {
				return map[int64]*ibasic.Product{1: {Name: "product-1"}}, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, authStore)

		list, err := m.FetchTokens(ctx, &TokenFilter{})
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, "product-1", list[0].Product.Name)
	})

	t.Run("storage error", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchTokensFn: func(ctx context.Context, param *TokenFilter) ([]*Token, error) {
				return nil, errors.New("db error")
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		_, err := m.FetchTokens(ctx, &TokenFilter{})
		require.Error(t, err)
	})
}

func TestAuthenticateManager_DeleteToken(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)

	store := &fakeAuthenticateStorager{
		deleteTokenFn: func(ctx context.Context, param *Token) error {
			return nil
		},
	}
	authStore := &fakeAuthorizeStorager{
		unbindTokenAllProductFn: func(ctx context.Context, token *Token) error {
			return nil
		},
	}
	m := NewAuthenticateManager(&fakeTxn{}, store, authStore)

	require.NoError(t, m.DeleteToken(ctx, &Token{ID: 1}))
}

func TestAuthenticateManager_CreateToken(t *testing.T) {
	ctx := context.Background()
	setupConfig(t)

	t.Run("success", func(t *testing.T) {
		created := &Token{ID: 1, Name: "token-1", Token: "abc", Scope: ScopeProduct}
		fetchCount := 0
		store := &fakeAuthenticateStorager{
			fetchTokensFn: func(ctx context.Context, param *TokenFilter) ([]*Token, error) {
				fetchCount++
				// call 1: name duplicate check; call 2: token duplicate check; call 3: fetch created token
				if fetchCount == 3 {
					return []*Token{created}, nil
				}
				return nil, nil
			},
			createTokenFn: func(ctx context.Context, token *TokenParam) error {
				return nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		token, err := m.CreateToken(ctx, &TokenParam{
			Name:  lib.PString("token-1"),
			Scope: lib.PString(ScopeProduct),
		}, nil)
		require.NoError(t, err)
		require.NotNil(t, token)
		assert.Equal(t, "token-1", token.Name)
	})

	t.Run("token name existed", func(t *testing.T) {
		store := &fakeAuthenticateStorager{
			fetchTokensFn: func(ctx context.Context, param *TokenFilter) ([]*Token, error) {
				if param.Name != nil {
					return []*Token{{Name: "token-1"}}, nil
				}
				return nil, nil
			},
		}
		m := NewAuthenticateManager(&fakeTxn{}, store, &fakeAuthorizeStorager{})

		_, err := m.CreateToken(ctx, &TokenParam{
			Name:  lib.PString("token-1"),
			Scope: lib.PString(ScopeProduct),
		}, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Token Record Existed")
	})
}
