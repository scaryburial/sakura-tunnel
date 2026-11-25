// Copyright 2026 Sakura Tunnel Authors
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

package auth

import (
	"fmt"
	"slices"
	"time"

	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/msg"
	"github.com/fatedier/frp/pkg/userdb"
)

// UserAuthVerifier verifies logins against a per-user token database instead of
// a single shared token, which is what makes the server multi-tenant.
type UserAuthVerifier struct {
	store                *userdb.Store
	additionalAuthScopes []v1.AuthScope
}

var _ Verifier = &UserAuthVerifier{}

func NewUserAuthVerifier(store *userdb.Store, additionalAuthScopes []v1.AuthScope) *UserAuthVerifier {
	return &UserAuthVerifier{store: store, additionalAuthScopes: additionalAuthScopes}
}

func (a *UserAuthVerifier) VerifyLogin(m *msg.Login) error {
	if a.store == nil {
		return fmt.Errorf("user database is not configured")
	}
	if m.User == "" {
		return fmt.Errorf("login rejected: client did not send a user name")
	}
	u, err := a.store.Authenticate(m.User, m.PrivilegeKey, m.Timestamp, time.Now())
	if err != nil {
		return fmt.Errorf("login rejected for user [%s]: %v", m.User, err)
	}
	a.store.Touch(u.Name)
	return nil
}

func (a *UserAuthVerifier) VerifyPing(m *msg.Ping) error {
	if !slices.Contains(a.additionalAuthScopes, v1.AuthScopeHeartBeats) {
		return nil
	}
	if a.store == nil {
		return fmt.Errorf("user database is not configured")
	}
	if err := a.store.AuthenticateAny(m.PrivilegeKey, m.Timestamp, time.Now()); err != nil {
		return fmt.Errorf("invalid heartbeat: %v", err)
	}
	return nil
}

func (a *UserAuthVerifier) VerifyNewWorkConn(m *msg.NewWorkConn) error {
	if !slices.Contains(a.additionalAuthScopes, v1.AuthScopeNewWorkConns) {
		return nil
	}
	if a.store == nil {
		return fmt.Errorf("user database is not configured")
	}
	if err := a.store.AuthenticateAny(m.PrivilegeKey, m.Timestamp, time.Now()); err != nil {
		return fmt.Errorf("invalid work connection: %v", err)
	}
	return nil
}
