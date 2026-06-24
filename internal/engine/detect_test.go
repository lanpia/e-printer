package engine

import "testing"

func TestDetectBytes(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		path string
		want Format
	}{
		{"pdf", []byte("%PDF-1.7\n..."), "a.pdf", FormatPDF},
		{"ps", []byte("%!PS-Adobe-3.0\n"), "a.ps", FormatPS},
		{"pclxl-pjl", []byte("\x1b%-12345X@PJL ENTER LANGUAGE = PCLXL\n"), "a.bin", FormatPXL},
		{"ps-pjl", []byte("\x1b%-12345X@PJL ENTER LANGUAGE=POSTSCRIPT\n"), "a.bin", FormatPS},
		{"pcl5-esc", []byte("\x1bE\x1b&l0O"), "a.bin", FormatPCL},
		{"pclxl-sig", []byte(") HP-PCL XL;1;1\n"), "a.bin", FormatPXL},
		{"ext-only-pdf", []byte("plain text no magic"), "x.pdf", FormatPDF},
		{"unknown", []byte("plain text no magic"), "x.dat", FormatUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := detectBytes(c.data, c.path); got != c.want {
				t.Errorf("detectBytes(%q) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestPageRangeArgs(t *testing.T) {
	cases := []struct {
		spec string
		want int // 생성되는 인자 개수
	}{
		{"", 0},
		{"5", 2},
		{"1-3", 2},
		{"2-", 1},
		{"-4", 1},
	}
	for _, c := range cases {
		if got := len(pageRangeArgs(c.spec)); got != c.want {
			t.Errorf("pageRangeArgs(%q) = %d args, want %d", c.spec, got, c.want)
		}
	}
}

func TestInsertPagePattern(t *testing.T) {
	if got := insertPagePattern("out.png"); got != "out-%03d.png" {
		t.Errorf("insertPagePattern = %q", got)
	}
	if got := insertPagePattern("noext"); got != "noext-%03d" {
		t.Errorf("insertPagePattern = %q", got)
	}
}
