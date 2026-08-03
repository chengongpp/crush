package tools

import "testing"

func TestCheckDangerousCommand(t *testing.T) {
	t.Parallel()

	blocked := map[string]string{
		"rm -rf /":                           "rm -rf /",
		"rm -rf /*":                          "rm -rf /",
		"rm -rfv /":                          "rm -rf /",
		"rm --no-preserve-root -rf /":        "rm -rf /",
		"rm -rf --no-preserve-root /":        "rm -rf /",
		"rm -rf / ; echo done":               "rm -rf /",
		"echo x && rm -rf / && echo done":    "rm -rf /",
		"sudo rm -rf /etc":                   "sudo rm -rf",
		"sudo rm -rfv /var/log":              "sudo rm -rf",
		"mkfs /dev/sdb":                      "mkfs",
		"mkfs.ext4 /dev/sdb":                 "mkfs",
		"dd if=/dev/zero of=/dev/sda":        "dd to raw device",
		"dd if=/dev/urandom of=/dev/nvme0n1": "dd to raw device",
		"dd of=/dev/sdb1":                    "dd to raw device",
		":(){ :|:& };:":                      "fork bomb",
		"> /dev/sda":                         "write to raw device",
		"echo hi >>/dev/sdb":                 "write to raw device",
		"echo data > /dev/nvme0n1p1":         "write to raw device",
	}

	for cmd, want := range blocked {
		cmd, want := cmd, want
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			got, ok := checkDangerousCommand(cmd)
			if !ok {
				t.Fatalf("expected %q to be blocked", cmd)
			}
			if got != want {
				t.Fatalf("expected hint %q, got %q", want, got)
			}
		})
	}
}

func TestCheckDangerousCommandAllowed(t *testing.T) {
	t.Parallel()

	allowed := []string{
		"rm -rf /tmp/build",
		"rm -rf ./dist",
		"rm -rf /tmp/foo/",
		"rm file",
		"rm -f /tmp/cache",
		"ls -la /",
		"cat /dev/sda",
		"dd if=/dev/zero of=/dev/null bs=1M count=1",
		"grep -rn rm /tmp",
		"echo fork bomb prevention",
		"git clean -fdx",
		"npm run build",
		"go install mvdan.cc/gofumpt@latest",
		"sudo apt-get update",
		"> /dev/null",
	}

	for _, cmd := range allowed {
		cmd := cmd
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			if _, ok := checkDangerousCommand(cmd); ok {
				t.Fatalf("expected %q to be allowed", cmd)
			}
		})
	}
}
