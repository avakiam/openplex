package docker

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidatePHPSettings(t *testing.T) {
	tests := []struct {
		name    string
		in      map[string]string
		mem     int
		want    map[string]string
		wantErr string
	}{
		{"defaults dropped", map[string]string{"memory_limit": "256m", "display_errors": "off", "upload_max_filesize": ""}, 512, map[string]string{}, ""},
		{"normalized", map[string]string{"memory_limit": "1g", "upload_max_filesize": "128M", "post_max_size": "130M", "display_errors": "true", "max_execution_time": "0300"},
			2048, map[string]string{"memory_limit": "1G", "upload_max_filesize": "128M", "post_max_size": "130M", "display_errors": "On", "max_execution_time": "300"}, ""},
		{"memory above container", map[string]string{"memory_limit": "1G"}, 512, nil, "memory_limit"},
		{"post below upload", map[string]string{"upload_max_filesize": "128M"}, 512, nil, "post_max_size"},
		{"unknown key", map[string]string{"auto_prepend_file": "/etc/passwd"}, 512, nil, "not a PHP setting"},
		{"injection in size", map[string]string{"memory_limit": "128M\nauto_prepend_file=/x"}, 512, nil, "memory_limit"},
		{"bad timezone", map[string]string{"date.timezone": "Mars/Base"}, 512, nil, "time zone"},
		{"timezone ok", map[string]string{"date.timezone": "Europe/Madrid"}, 512, map[string]string{"date.timezone": "Europe/Madrid"}, ""},
		{"function list", map[string]string{"disable_functions": " exec, shell_exec ,system"}, 512, map[string]string{"disable_functions": "exec,shell_exec,system"}, ""},
		{"bad function list", map[string]string{"disable_functions": "exec;system"}, 512, nil, "function names"},
		{"int range", map[string]string{"max_input_vars": "5"}, 512, nil, "max_input_vars"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidatePHPSettings(tt.in, tt.mem)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Fatalf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestPHPIni(t *testing.T) {
	ini := PHPIni(map[string]string{"memory_limit": "1G", "date.timezone": "Europe/Madrid", "auto_prepend_file": "/x"}, 512)
	for _, want := range []string{"memory_limit = 512M\n", "date.timezone = \"Europe/Madrid\"\n"} {
		if !strings.Contains(ini, want) {
			t.Errorf("ini lacks %q:\n%s", want, ini)
		}
	}
	if strings.Contains(ini, "auto_prepend_file") {
		t.Error("unvalidated key rendered")
	}
}

func TestSpecWithoutPHPSettings(t *testing.T) {
	// Websites without PHP settings must keep their container across the
	// upgrade: the new fields may not change their spec (and so its hash).
	b, err := json.Marshal(siteSpec{Image: "x", UID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "PHP") {
		t.Fatalf("PHP fields serialized for a website without settings: %s", b)
	}
}
