package weixin

import (
	"strings"
	"testing"
)

func TestSummarizeDelimitedFile(t *testing.T) {
	summary := summarizeDelimitedFile("markers.csv", []byte("gene,score\nCD3D,1.2\nMS4A1,0.9\n"))
	if !strings.Contains(summary, "共 3 行，2 列") {
		t.Fatalf("expected row/col summary, got %q", summary)
	}
	if !strings.Contains(summary, "检测到列名（前 2 列）：gene | score") {
		t.Fatalf("expected detected columns, got %q", summary)
	}
	if !strings.Contains(summary, "前 2 行预览（前 2 列，不含表头）：CD3D | 1.2 ; MS4A1 | 0.9") {
		t.Fatalf("expected preview to include data rows without header, got %q", summary)
	}
}

func TestSummarizeDelimitedFileFallsBackWhenNoHeaderDetected(t *testing.T) {
	summary := summarizeDelimitedFile("matrix.tsv", []byte("CD3D\t1.2\nMS4A1\t0.9\nLYZ\t0.7\n"))
	if !strings.Contains(summary, "未可靠检测到列名") {
		t.Fatalf("expected no-header fallback, got %q", summary)
	}
	if !strings.Contains(summary, "CD3D | 1.2") {
		t.Fatalf("expected raw row preview, got %q", summary)
	}
}

func TestSummarizeDelimitedFileAppliesPreviewLimits(t *testing.T) {
	summary := summarizeDelimitedFile("wide.csv", []byte(strings.Join([]string{
		"c1,c2,c3,c4,c5,c6",
		"r1a,r1b,r1c,r1d,r1e,r1f",
		"r2a,r2b,r2c,r2d,r2e,r2f",
		"r3a,r3b,r3c,r3d,r3e,r3f",
		"r4a,r4b,r4c,r4d,r4e,r4f",
		"r5a,r5b,r5c,r5d,r5e,r5f",
		"r6a,r6b,r6c,r6d,r6e,r6f",
		"r7a,r7b,r7c,r7d,r7e,r7f",
		"r8a,r8b,r8c,r8d,r8e,r8f",
		"r9a,r9b,r9c,r9d,r9e,r9f",
		"r10a,r10b,r10c,r10d,r10e,r10f",
		"r11a,r11b,r11c,r11d,r11e,r11f",
	}, "\n")))
	if !strings.Contains(summary, "检测到列名（前 5 列）：c1 | c2 | c3 | c4 | c5") {
		t.Fatalf("expected preview column limit, got %q", summary)
	}
	if strings.Contains(summary, "c6") || strings.Contains(summary, "r1f") {
		t.Fatalf("expected summary to omit sixth column, got %q", summary)
	}
	if strings.Contains(summary, "r11a") {
		t.Fatalf("expected summary to omit the eleventh preview row, got %q", summary)
	}
}
