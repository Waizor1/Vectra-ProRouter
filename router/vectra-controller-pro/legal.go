// SPDX-License-Identifier: LicenseRef-Vectra-Proprietary
// Copyright (c) 2026 Vectra. All rights reserved.

// Package legal carries the package's license texts so the shipped binary can
// print them. It lives at the module root because go:embed cannot reference a
// parent directory, and LICENSE/NOTICE live there.
package legal

import _ "embed"

//go:embed LICENSE
var License string

//go:embed NOTICE
var Notice string
