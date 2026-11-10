package docker

import (
	"testing"

	"openplex.dev/openplex/internal/db"
)

func TestMariaGrantEscapesWildcards(t *testing.T) {
	got := mariaGrant(&db.Database{Name: "ali_ice_wp", Username: "ali_ice"})
	want := "GRANT ALL PRIVILEGES ON `ali\\_ice\\_wp`.* TO 'ali_ice'@'%'"
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}
