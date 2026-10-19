// SPDX-License-Identifier: LicenseRef-Vectra-Proprietary
// Copyright (c) 2026 Vectra. All rights reserved.

package setup

import "testing"

func TestEnrollmentTokenReadsTheUCIOption(t *testing.T) {
	r := newRouter(t)
	// Absent file / section / option all read back as "".
	if got := EnrollmentToken(r.env); got != "" {
		t.Fatalf("enrollment token with no config = %q, want empty", got)
	}
	r.write(t, r.env.VectraConfig, "config controller 'main'\n\toption enabled '1'\n")
	if got := EnrollmentToken(r.env); got != "" {
		t.Fatalf("enrollment token with option absent = %q, want empty", got)
	}
	r.write(t, r.env.VectraConfig, "config controller 'main'\n\toption enrollment_token 'vpt1.1791460800.abc'\n")
	if got, want := EnrollmentToken(r.env), "vpt1.1791460800.abc"; got != want {
		t.Fatalf("enrollment token = %q, want %q", got, want)
	}
	// Surrounding whitespace is trimmed.
	r.write(t, r.env.VectraConfig, "config controller 'main'\n\toption enrollment_token '  vpt1.x.y  '\n")
	if got, want := EnrollmentToken(r.env), "vpt1.x.y"; got != want {
		t.Fatalf("enrollment token = %q, want %q (trimmed)", got, want)
	}
}
