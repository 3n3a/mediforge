package dispatch

import (
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
			name:   "match one dir with trailing slash",
			files:  []string{"/tv/A/a.mkv", "/tv/B/b.mkv", "/tv/A/c.mkv"},
			prefix: "/tv/A/",
			want:   []string{"/tv/A/a.mkv", "/tv/A/c.mkv"},
		},
		{
			name:   "match one dir without trailing slash (normalized)",
			files:  []string{"/tv/A/a.mkv", "/tv/AB/b.mkv"},
			prefix: "/tv/A",
			want:   []string{"/tv/A/a.mkv"},
		},
		{
			name:   "no match",
			files:  []string{"/tv/A/a.mkv", "/tv/B/b.mkv"},
			prefix: "/tv/C/",
			want:   []string{},
		},
		{
			name:   "exact file match",
			files:  []string{"/tv/A/file.mkv", "/tv/A/file.mkv.tmp"},
			prefix: "/tv/A/file.mkv",
			want:   []string{"/tv/A/file.mkv"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterByPrefix(tc.files, tc.prefix)
			if !sliceEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
