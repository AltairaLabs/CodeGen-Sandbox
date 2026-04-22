package tools

import "testing"

// TestDenyReason_AllTokens exercises every alternative in the denylist and a
// few boundary cases. These run directly against denyReason (no bash spawn),
// so they're ~1000x faster than the handler-level TestBashDeny_* tests and
// are safe to run anywhere.
func TestDenyReason_AllTokens(t *testing.T) {
	match := []string{
		"sudo whoami",
		"su -l myuser",
		"shutdown -h now",
		"reboot",
		"halt",
		"poweroff",
		"chroot /tmp/jail /bin/sh",
		"mount -t tmpfs none /mnt",
		"umount /mnt",
		"mkfs /dev/null",
		"mkfs.ext4 /dev/null",
		"mkfs.xfs /dev/null",
		// Command-position variants:
		"echo x && sudo ls", // after &&
		"echo x | sudo ls",  // after |
		"echo x ; sudo ls",  // after ;
		"(sudo ls)",         // after (
		"\nsudo ls",         // after \n (within \s)
	}
	for _, cmd := range match {
		t.Run("match:"+cmd, func(t *testing.T) {
			if denyReason(cmd) == "" {
				t.Errorf("expected deny, got empty; command=%q", cmd)
			}
		})
	}

	noMatch := []string{
		"",                   // empty string
		"ls /etc/sudoers",    // sudoers is a filename, not a command
		"echo pseudo-random", // pseudo doesn't match su alternation
		"echo sudoku",        // sudoku doesn't match sudo alternation
		"echo 'don't sudo'",  // quoted sudo is intentionally not caught
		"mkfs.",              // trailing dot, no variant — no match
		"cat /tmp/su.txt",    // su preceded by /, not boundary
		"cat unmount.txt",    // umount inside a word
	}
	for _, cmd := range noMatch {
		t.Run("no-match:"+cmd, func(t *testing.T) {
			if reason := denyReason(cmd); reason != "" {
				t.Errorf("unexpected deny: %q for command=%q", reason, cmd)
			}
		})
	}
}
