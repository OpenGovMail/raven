package maildomain

import "testing"

func TestFromHandle(t *testing.T) {
	tests := []struct {
		name   string
		handle string
		want   string
		wantOK bool
	}{
		{name: "two labels", handle: "lsf.lk", want: "lsf.lk", wantOK: true},
		{name: "three labels", handle: "silver.lsf.lk", want: "silver.lsf.lk", wantOK: true},
		{name: "public suffix", handle: "example.co.uk", want: "example.co.uk", wantOK: true},
		{name: "deep", handle: "a.b.c.opensource.lk", want: "a.b.c.opensource.lk", wantOK: true},
		{name: "mixed case", handle: "Silver.LSF.lk", want: "silver.lsf.lk", wantOK: true},
		{name: "trailing dot", handle: "lsf.lk.", want: "lsf.lk", wantOK: true},
		{name: "surrounding space", handle: " lsf.lk ", want: "lsf.lk", wantOK: true},
		{name: "hyphen and digits", handle: "open-data2.lk", want: "open-data2.lk", wantOK: true},
		{name: "container OU", handle: "foundations"},
		{name: "system OU", handle: "default"},
		{name: "empty", handle: ""},
		{name: "only a dot", handle: "."},
		{name: "empty label", handle: "silver..lsf.lk"},
		{name: "leading dot", handle: ".lsf.lk"},
		{name: "slash", handle: "lsf.lk/silver"},
		{name: "backslash", handle: `lsf.lk\silver`},
		{name: "traversal", handle: "../lsf.lk"},
		{name: "plus", handle: "lsf.lk+ldf.lk"},
		{name: "inner space", handle: "lsf .lk"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FromHandle(tt.handle)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("FromHandle(%q) = (%q, %v), want (%q, %v)", tt.handle, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
