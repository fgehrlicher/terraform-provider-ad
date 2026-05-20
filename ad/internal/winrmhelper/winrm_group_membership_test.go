package winrmhelper

import (
	"fmt"
	"testing"
)

func makeMembers(n int) []*GroupMember {
	out := make([]*GroupMember, n)
	for i := 0; i < n; i++ {
		out[i] = &GroupMember{GUID: fmt.Sprintf("guid-%05d", i)}
	}
	return out
}

func TestChunkGroupMembers(t *testing.T) {
	cases := []struct {
		name        string
		total       int
		size        int
		wantBatches int
		wantLastLen int
	}{
		{"empty input returns one empty batch", 0, 50, 1, 0},
		{"smaller than batch fits in one", 17, 50, 1, 17},
		{"exact multiple", 100, 50, 2, 50},
		{"non-exact multiple keeps remainder", 161, 50, 4, 11},
		{"size larger than input", 5, 50, 1, 5},
		{"size of one yields one batch per member", 3, 1, 3, 1},
		{"zero size falls back to single batch", 10, 0, 1, 10},
		{"negative size falls back to single batch", 10, -3, 1, 10},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			members := makeMembers(tc.total)
			batches := chunkGroupMembers(members, tc.size)

			if len(batches) != tc.wantBatches {
				t.Fatalf("got %d batches, want %d", len(batches), tc.wantBatches)
			}
			if len(batches[len(batches)-1]) != tc.wantLastLen {
				t.Fatalf("last batch len = %d, want %d", len(batches[len(batches)-1]), tc.wantLastLen)
			}

			seen := 0
			for i, b := range batches {
				if i < len(batches)-1 && tc.size > 0 && len(b) != tc.size {
					t.Fatalf("batch %d len = %d, want %d (non-final batch)", i, len(b), tc.size)
				}
				for _, m := range b {
					if m.GUID != members[seen].GUID {
						t.Fatalf("order broken at index %d: got %s, want %s", seen, m.GUID, members[seen].GUID)
					}
					seen++
				}
			}
			if seen != tc.total {
				t.Fatalf("covered %d members across batches, want %d", seen, tc.total)
			}
		})
	}
}

func TestChunkGroupMembersBatchSizeFitsCommandLineLimit(t *testing.T) {
	// Synthesise the worst-case PowerShell script a single batch produces and
	// verify the encoded command stays under Windows' 8191-char CreateProcess
	// cap. PowerShell is dispatched as `powershell.exe -EncodedCommand <b64>`
	// where the script is UTF-16LE encoded then base64'd (~2.67x expansion).
	const (
		// Generous credential prelude: long UPN + long password + long server.
		userLen     = 128
		passwordLen = 128
		serverLen   = 253 // RFC 1035 max FQDN length
		groupGUID   = "fed65087-a360-4539-932e-69a148d34979"
		// powershell.exe -EncodedCommand <b64> ; budget for the exe + flag.
		argvOverhead = 64
		// Windows CreateProcess command-line ceiling.
		cmdLineLimit = 8191
	)

	batch := makeMembers(groupMembershipBatchSize)
	memberList := getMembershipList(batch)
	cmd := fmt.Sprintf("Remove-ADGroupMember -Identity %q -Members %s -Confirm:$false", groupGUID, memberList)

	// Mirror the credential prelude assembled by NewPSCommand when
	// PassCredentials is enabled (powershell_command.go:43-63).
	prelude := fmt.Sprintf(
		"$Password = ConvertTo-SecureString -String \"%s\" -AsPlainText -Force\n"+
			"$User = \"%s\"\n"+
			"$Credential = New-Object -TypeName System.Management.Automation.PSCredential -ArgumentList $User, $Password\n",
		repeat("p", passwordLen), repeat("u", userLen),
	)
	suffix := fmt.Sprintf(" -Credential $Credential -Server %s", repeat("s", serverLen))
	script := prelude + cmd + suffix

	// UTF-16LE doubles, base64 is ceil(n/3)*4.
	utf16Bytes := len(script) * 2
	b64Len := ((utf16Bytes + 2) / 3) * 4
	encodedCmdLine := b64Len + argvOverhead

	t.Logf("script=%d bytes, utf16=%d, base64=%d, total argv ~= %d (limit %d)",
		len(script), utf16Bytes, b64Len, encodedCmdLine, cmdLineLimit)

	if encodedCmdLine >= cmdLineLimit {
		t.Fatalf("worst-case encoded command line %d >= %d; lower groupMembershipBatchSize", encodedCmdLine, cmdLineLimit)
	}
}

func repeat(s string, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = s[0]
	}
	return string(out)
}
