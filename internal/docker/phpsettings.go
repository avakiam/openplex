package docker

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // validate time zones even on hosts without zoneinfo

	"openplex.dev/openplex/internal/msg"
)

// PHPOption is a PHP setting a website may change. The defaults are the
// ones baked into the PHP images (php/openplex.ini and production php.ini).
type PHPOption struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"` // size (MB/GB), int, bool, timezone, list
	Default string `json:"default"`
	Min     int64  `json:"min,omitempty"` // size in MB, or int value
	Max     int64  `json:"max,omitempty"` // 0 for memory_limit: the website's memory limit
}

// PHPOptions lists the settings in display order.
var PHPOptions = []PHPOption{
	{Key: "memory_limit", Kind: "size", Default: "256M", Min: 32},
	{Key: "upload_max_filesize", Kind: "size", Default: "64M", Min: 1, Max: 2048},
	{Key: "post_max_size", Kind: "size", Default: "64M", Min: 1, Max: 2048},
	{Key: "max_execution_time", Kind: "int", Default: "60", Min: 1, Max: 3600},
	{Key: "max_input_time", Kind: "int", Default: "60", Min: 1, Max: 3600},
	{Key: "max_input_vars", Kind: "int", Default: "3000", Min: 100, Max: 100000},
	{Key: "session.gc_maxlifetime", Kind: "int", Default: "1440", Min: 60, Max: 604800},
	{Key: "display_errors", Kind: "bool", Default: "Off"},
	{Key: "allow_url_fopen", Kind: "bool", Default: "On"},
	{Key: "date.timezone", Kind: "timezone", Default: "UTC"},
	{Key: "disable_functions", Kind: "list", Default: ""},
}

var (
	sizeRe = regexp.MustCompile(`^([0-9]{1,5})([MG])$`)
	listRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(,[A-Za-z_][A-Za-z0-9_]*)*$`)
)

// sizeMB parses "256M" / "2G" into megabytes.
func sizeMB(v string) (int64, bool) {
	m := sizeRe.FindStringSubmatch(v)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	if m[2] == "G" {
		n *= 1024
	}
	return n, true
}

// ValidatePHPSettings checks and normalizes settings for a website with
// memoryMB of memory. Unknown keys are refused; values equal to the default
// are dropped, so only real changes are stored.
func ValidatePHPSettings(in map[string]string, memoryMB int) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range in {
		i := slices.IndexFunc(PHPOptions, func(o PHPOption) bool { return o.Key == k })
		if i < 0 {
			return nil, msg.New("{setting} is not a PHP setting that can be changed", "setting", k)
		}
		o := PHPOptions[i]
		v = strings.TrimSpace(v)
		if v == "" && o.Kind != "list" {
			continue // back to the default
		}
		switch o.Kind {
		case "size":
			v = strings.ToUpper(v)
			mb, ok := sizeMB(v)
			max := o.Max
			if max == 0 {
				max = int64(memoryMB)
			}
			if !ok || mb < o.Min || mb > max {
				return nil, msg.New("{setting} must be between {min}M and {max}M (e.g. 128M or 1G)", "setting", k, "min", o.Min, "max", max)
			}
		case "int":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < o.Min || n > o.Max {
				return nil, msg.New("{setting} must be a whole number between {min} and {max}", "setting", k, "min", o.Min, "max", o.Max)
			}
			v = strconv.FormatInt(n, 10)
		case "bool":
			switch strings.ToLower(v) {
			case "on", "1", "true", "yes":
				v = "On"
			case "off", "0", "false", "no":
				v = "Off"
			default:
				return nil, msg.New("{setting} must be On or Off", "setting", k)
			}
		case "timezone":
			if _, err := time.LoadLocation(v); err != nil || v == "Local" || len(v) > 64 {
				return nil, msg.New("{setting} must be a time zone such as Europe/Madrid", "setting", k)
			}
		case "list":
			v = strings.Join(strings.Fields(strings.ReplaceAll(v, ",", " ")), ",")
			if v != "" && (!listRe.MatchString(v) || len(v) > 2000) {
				return nil, msg.New("{setting} must be a comma-separated list of function names", "setting", k)
			}
		}
		if v != o.Default {
			out[k] = v
		}
	}
	upload, _ := sizeMB(effective(out, "upload_max_filesize"))
	post, _ := sizeMB(effective(out, "post_max_size"))
	if post < upload {
		return nil, errors.New("post_max_size must be at least upload_max_filesize, or uploads of that size fail")
	}
	return out, nil
}

func effective(s map[string]string, key string) string {
	if v, ok := s[key]; ok {
		return v
	}
	for _, o := range PHPOptions {
		if o.Key == key {
			return o.Default
		}
	}
	return ""
}

// PHPIni renders validated settings as an ini file for the website's
// container. memory_limit never exceeds the container's memory, even if the
// website's memory was lowered after it was set.
func PHPIni(s map[string]string, memoryMB int) string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("; Written by OpenPlex from the website's PHP settings.\n")
	for _, k := range keys {
		v := s[k]
		if k == "memory_limit" {
			if mb, ok := sizeMB(v); ok && mb > int64(memoryMB) {
				v = strconv.Itoa(memoryMB) + "M"
			}
		}
		if slices.IndexFunc(PHPOptions, func(o PHPOption) bool { return o.Key == k }) < 0 || strings.ContainsAny(v, "\"\n\r;") {
			continue // only validated keys and values ever reach the file
		}
		if k == "date.timezone" || k == "disable_functions" {
			v = `"` + v + `"`
		}
		fmt.Fprintf(&b, "%s = %s\n", k, v)
	}
	return b.String()
}
