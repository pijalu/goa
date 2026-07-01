// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import "github.com/pijalu/goa/internal/toolaccess"

// ToolAccess is a type alias for toolaccess.Access.
// Tools implement the Accessor interface to declare resource access for
// the concurrent tool scheduler.
type ToolAccess = toolaccess.Access

// Accessor is the interface tools implement to declare resource access.
type Accessor = toolaccess.Accessor
