package backup

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

// fakeHost builds /proc/self/mountinfo and a /sys block tree under a temp root:
// sda (partitions sda1, sda2), sdb (sdb1), sdc + sdd in an md0 RAID, and an
// LVM volume dm-0 on sda2.
func fakeHost(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(p string) {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		mk(filepath.Dir(name))
		if err := os.Symlink(filepath.Join(root, target), filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	touch := func(p string) {
		mk(filepath.Dir(p))
		if err := os.WriteFile(filepath.Join(root, p), []byte("1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dev := "sys/devices/pci0/"
	for _, d := range []string{"sda/sda1", "sda/sda2", "sdb/sdb1"} {
		mk(dev + "block/" + d)
		touch(dev + "block/" + d + "/partition")
	}
	mk(dev + "block/sdc")
	mk(dev + "block/sdd")
	mk("sys/devices/virtual/block/md0/slaves")
	link(dev+"block/sdc", "sys/devices/virtual/block/md0/slaves/sdc")
	link(dev+"block/sdd", "sys/devices/virtual/block/md0/slaves/sdd")
	mk("sys/devices/virtual/block/dm-0/slaves")
	link(dev+"block/sda/sda2", "sys/devices/virtual/block/dm-0/slaves/sda2")
	for name, target := range map[string]string{
		"sda1": dev + "block/sda/sda1", "sda2": dev + "block/sda/sda2", "sdb1": dev + "block/sdb/sdb1",
		"sdc": dev + "block/sdc", "sdd": dev + "block/sdd", "md0": "sys/devices/virtual/block/md0", "dm-0": "sys/devices/virtual/block/dm-0",
	} {
		link(target, "sys/class/block/"+name)
	}
	for mm, name := range map[string]string{"8:1": "sda1", "8:17": "sdb1", "9:0": "md0", "253:0": "dm-0"} {
		link("sys/class/block/"+name, "sys/dev/block/"+mm)
	}
	link("sys/devices/virtual/block/dm-0", "dev/dm-0")
	link("dev/dm-0", "dev/mapper/vg-data")
	for _, p := range []string{"var/lib/openplex", "mnt/usb", "mnt/raid", "mnt/nas", "mnt/btr", "mnt/lv", "mnt/tank", "home/my backups"} {
		mk(p)
	}
	touch("proc/self/mountinfo")
	info := `22 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw
30 22 8:17 / /mnt/usb rw - ext4 /dev/sdb1 rw
31 22 9:0 / /mnt/raid rw - xfs /dev/md0 rw
32 22 0:55 / /mnt/nas rw - nfs4 192.168.0.5:/backups rw
33 22 0:60 /@data /mnt/btr rw - btrfs /dev/mapper/vg-data rw
34 22 253:0 / /mnt/lv rw - ext4 /dev/mapper/vg-data rw
35 22 0:70 / /mnt/tank rw - zfs tank/backups rw
36 22 0:80 / /home/my\040backups rw - fuse.sshfs me@nas:/b rw
`
	if err := os.WriteFile(filepath.Join(root, "proc/self/mountinfo"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLocalOffsite(t *testing.T) {
	root := fakeHost(t)
	data := "/var/lib/openplex"
	for _, tt := range []struct {
		repo, level, detail string
	}{
		{"/var/lib/openplex/backups/repo", OffsiteSameDisk, "sda"}, // default: inside the data dir, not created yet
		{"/srv/backups", OffsiteSameDisk, "sda"},
		{"/mnt/usb/openplex", OffsiteOtherDisk, "sdb"},
		{"/mnt/raid/openplex", OffsiteOtherDisk, "sdc, sdd"},
		{"/mnt/nas/openplex", OffsiteRemote, "nfs4 192.168.0.5:/backups"},
		{"/home/my backups/x", OffsiteRemote, "fuse.sshfs me@nas:/b"},
		{"/mnt/lv/openplex", OffsiteSameDisk, "sda"},  // LVM on another partition of the same disk
		{"/mnt/btr/openplex", OffsiteSameDisk, "sda"}, // btrfs (anonymous device) on that volume
		{"/mnt/tank/openplex", OffsiteOtherDisk, "zfs:tank"},
	} {
		got := LocalOffsite(root, tt.repo, data)
		if got.Level != tt.level || got.Detail != tt.detail {
			t.Errorf("%s: %+v, want %s %q", tt.repo, got, tt.level, tt.detail)
		}
	}
	if got := LocalOffsite(t.TempDir(), "/x", data); got.Level != OffsiteUnknown {
		t.Errorf("no mountinfo: %+v", got)
	}
}

func TestS3Offsite(t *testing.T) {
	local := []netip.Addr{netip.MustParseAddr("192.0.2.10")}
	ctx := context.Background()
	for _, tt := range []struct{ endpoint, level string }{
		{"http://127.0.0.1:9000", OffsiteSameServer},
		{"https://192.0.2.10", OffsiteSameServer},
		{"http://[::1]:9000", OffsiteSameServer},
		{"https://198.51.100.20", OffsiteRemote},
		{"https://localhost:9000", OffsiteSameServer},
	} {
		if got := S3Offsite(ctx, tt.endpoint, local); got.Level != tt.level {
			t.Errorf("%s: %+v, want %s", tt.endpoint, got, tt.level)
		}
	}
}
