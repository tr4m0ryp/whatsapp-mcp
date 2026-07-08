package wa

import (
	"strings"
	"testing"
)

func TestExtractDirectPathFromURL(t *testing.T) {
	cases := []struct {
		name                   string
		input                  string
		want                   string
		requireSingleSlashPath bool
	}{
		{
			name:                   "preserves WhatsApp CDN auth query",
			input:                  "https://mmg.whatsapp.net/v/t62.7118-24/13812002_698058036224062_3424455886509161511_n.enc?ccb=11-4&oh=01_Q5Aa&oe=6854A1B2",
			want:                   "/v/t62.7118-24/13812002_698058036224062_3424455886509161511_n.enc?ccb=11-4&oh=01_Q5Aa&oe=6854A1B2",
			requireSingleSlashPath: true,
		},
		{
			name:  "keeps fallback for URLs without WhatsApp CDN marker",
			input: "https://example.com/v/t62.7118-24/file.enc?ccb=11-4&oh=01_Q5Aa&oe=6854A1B2",
			want:  "https://example.com/v/t62.7118-24/file.enc?ccb=11-4&oh=01_Q5Aa&oe=6854A1B2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractDirectPathFromURL(tc.input)
			if got != tc.want {
				t.Fatalf("extractDirectPathFromURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if tc.requireSingleSlashPath && (!strings.HasPrefix(got, "/") || strings.HasPrefix(got, "//")) {
				t.Fatalf("expected exactly one leading slash in %q", got)
			}
		})
	}
}
