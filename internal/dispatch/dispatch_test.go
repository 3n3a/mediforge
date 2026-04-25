package dispatch

import (
	"reflect"
	"testing"
)

func TestFilterByPrefix(t *testing.T) {
	cases := []struct {
		name   string
		files  []string
		prefix string
		want   []string
	}{
		{
			name:   "empty prefix returns all",
			files:  []string{"/tv/A/a.mkv", "/tv/B/b.mkv"},
			prefix: "",
			want:   []string{"/tv/A/a.mkv", "/tv/B/b.mkv"},
		},
		{
			name:   "match one dir",
			files:  []string{"/tv/A/a.mkv", "/tv/B/b.mkv", "/tv/A/c.mkv"},
			prefix: "/tv/A/",
			want:   []string{"/tv/A/a.mkv", "/tv/A/c.mkv"},
		},
		{
			name:   "no match",
			files:  []string{"/tv/A/a.mkv", "/tv/B/b.mkv"},
			prefix: "/tv/C/",
			want:   []string{},
		},
		{
			name:   "prefix without trailing slash",
			files:  []string{"/tv/A/a.mkv", "/tv/AB/b.mkv"},
			prefix: "/tv/A",
			want:   []string{"/tv/A/a.mkv", "/tv/AB/b.mkv"},
		},
		{
			name:   "prefix with trailing slash is precise",
			files:  []string{"/tv/A/a.mkv", "/tv/AB/b.mkv"},
			prefix: "/tv/A/",
			want:   []string{"/tv/A/a.mkv"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterByPrefix(tc.files, tc.prefix)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// filterByPrefix filters paths to those starting with prefix.
// This is the same logic used in runLibrary.
func filterByPrefix(files []string, prefix string) []string {
	if prefix == "" {
		return files
	}
	filtered := files[:0]
	for _, f := range files {
		if len(f) >= len(prefix) && f[:len(prefix)] == prefix {
			filtered = append(filtered, f)
		}
	}
	return filtered
}
