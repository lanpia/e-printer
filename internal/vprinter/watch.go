package vprinter

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ConvertFunc 는 스풀 파일(in)을 PDF(out)로 변환하는 함수이다.
type ConvertFunc func(in, out string) error

// Watcher 는 포트 파일을 감시하다가 새 인쇄 작업이 떨어지면 PDF 로 변환한다.
type Watcher struct {
	OutputDir string      // PDF 자동 저장 폴더
	Convert   ConvertFunc // 변환 콜백 (보통 engine.Convert 래핑)
	OnEvent   func(msg string)
	Interval  time.Duration // 0이면 800ms

	stop chan struct{}
}

// Stop 은 감시 루프를 종료시킨다.
func (w *Watcher) Stop() {
	if w.stop != nil {
		close(w.stop)
	}
}

func (w *Watcher) emit(format string, a ...interface{}) {
	if w.OnEvent != nil {
		w.OnEvent(fmt.Sprintf(format, a...))
	}
}

// Run 은 감시 루프를 시작한다(블로킹). 별도 고루틴에서 호출할 것.
func (w *Watcher) Run() {
	if w.Interval <= 0 {
		// 로컬 포트 파일은 인쇄 완료 후 스풀러가 약 1초 만에 삭제하므로
		// 그 짧은 창 안에 가로채려면 빠르게 폴링해야 한다.
		w.Interval = 200 * time.Millisecond
	}
	w.stop = make(chan struct{})
	port := PortFile()
	var lastSize int64 = -1

	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
		}

		fi, err := os.Stat(port)
		if err != nil || fi.Size() == 0 {
			lastSize = -1
			continue
		}
		// 두 번 연속 같은 크기일 때만(쓰기 완료 추정) 처리한다.
		if fi.Size() != lastSize {
			lastSize = fi.Size()
			continue
		}
		lastSize = -1

		// 포트 파일을 처리용 이름으로 가로채기(원자적 rename). 잠겨 있으면 다음 틱에 재시도.
		job := filepath.Join(SpoolDir(), "job-"+time.Now().Format("20060102_150405.000")+".ps")
		if err := os.Rename(port, job); err != nil {
			continue
		}

		out := uniqueName(w.OutputDir, time.Now())
		w.emit("인쇄 작업 감지 → 변환 중…")
		if err := w.Convert(job, out); err != nil {
			bad := job + ".err"
			os.Rename(job, bad)
			w.emit("변환 실패: %v (원본 보관: %s)", err, bad)
			continue
		}
		os.Remove(job)
		w.emit("PDF 저장 완료: %s", out)
	}
}

// uniqueName 은 저장 폴더에서 충돌하지 않는 PDF 경로를 만든다.
func uniqueName(dir string, t time.Time) string {
	base := "eprinter_" + t.Format("20060102_150405")
	p := filepath.Join(dir, base+".pdf")
	for i := 2; ; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s_%d.pdf", base, i))
	}
}
