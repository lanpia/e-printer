package vprinter

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// tcpHost 는 가상 프린터(Standard TCP/IP 포트)가 가리키는 주소이다.
// 항상 루프백 — 인쇄 데이터는 이 PC 안에서만 오간다.
const tcpHost = "127.0.0.1"

// TCPPort 는 가상 프린터와 로컬 리스너가 함께 쓰는 포트이다.
// 기본 9100(RAW 인쇄 표준). 환경변수 EPRINTER_TCP_PORT 로 바꿀 수 있다.
func TCPPort() int {
	if v := strings.TrimSpace(os.Getenv("EPRINTER_TCP_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	return 9100
}

// ListenAddr 는 리스너가 바인딩할 주소("127.0.0.1:9100")이다.
func ListenAddr() string { return fmt.Sprintf("%s:%d", tcpHost, TCPPort()) }

// SaveFunc 는 한 인쇄 작업의 원시 데이터(data)를 out 경로의 PDF 로 저장한다.
// data 는 드라이버가 보낸 그대로다(Microsoft Print To PDF → 이미 PDF).
type SaveFunc func(data []byte, out string) error

// Server 는 가상 프린터의 TCP/IP 포트로 들어오는 인쇄 작업을 받아 PDF 로 저장한다.
// 인쇄 1건 = TCP 연결 1개. 연결이 닫히면(EOF) 한 작업의 데이터가 끝난 것이다.
type Server struct {
	OutputDir string  // PDF 자동 저장 폴더
	Save      SaveFunc // 작업 저장 콜백
	OnEvent   func(string)

	ln   net.Listener
	stop chan struct{}
}

func (s *Server) emit(format string, a ...interface{}) {
	if s.OnEvent != nil {
		s.OnEvent(fmt.Sprintf(format, a...))
	}
}

// Start 는 로컬 TCP 리스너를 띄운다(논블로킹, 별도 고루틴에서 수신).
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", ListenAddr())
	if err != nil {
		return fmt.Errorf("TCP 리스너(%s) 시작 실패: %w", ListenAddr(), err)
	}
	s.ln = ln
	s.stop = make(chan struct{})
	go s.acceptLoop()
	return nil
}

// Stop 은 리스너를 종료한다.
func (s *Server) Stop() {
	if s.stop != nil {
		close(s.stop)
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.stop:
				return // 정상 종료
			default:
				continue
			}
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	data, err := io.ReadAll(conn) // 연결이 닫힐 때까지 = 한 작업 전체
	if err != nil || len(data) == 0 {
		return
	}
	s.emit("인쇄 작업 수신 → 저장 중…")
	out := uniqueName(s.OutputDir, time.Now())
	if s.Save != nil {
		if err := s.Save(data, out); err != nil {
			s.emit("저장 실패: %v", err)
			return
		}
	}
	s.emit("PDF 저장 완료: %s", out)
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
