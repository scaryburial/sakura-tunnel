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

// Package webui embeds the hand written Chinese dashboard. It is a few tens of
// kilobytes of static files instead of the upstream Vue bundle, which keeps the
// server binary small and removes the Node.js build step entirely.
package webui

import (
	"embed"

	"github.com/fatedier/frp/assets"
)

//go:embed dist
var files embed.FS

func init() {
	assets.Register(files)
}
