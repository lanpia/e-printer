package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"eprinter/internal/engine"
	"eprinter/internal/vprinter"
)

// runCLI 는 서브커맨드를 처리한다.
func runCLI(args []string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch args[0] {
	case "info":
		err = cmdInfo()
	case "printers":
		err = cmdPrinters(ctx)
	case "convert":
		err = cmdConvert(ctx, args[1:])
	case "print":
		err = cmdPrint(ctx, args[1:])
	case "install-printer":
		err = cmdInstallPrinter()
	case "remove-printer":
		err = cmdRemovePrinter()
	case "gui":
		guiMain()
		return
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "알 수 없는 명령: %q\n\n", args[0])
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		os.Exit(1)
	}
}

func cmdInstallPrinter() error {
	if !vprinter.IsAdmin() {
		return fmt.Errorf("관리자 권한이 필요합니다. 관리자 PowerShell 에서 실행하세요")
	}
	if err := vprinter.Install(); err != nil {
		return err
	}
	fmt.Printf("가상 프린터 설치 완료: %q\nTCP/IP 포트: %s (인쇄 데이터를 받으려면 GUI 의 리스너가 떠 있어야 함)\n",
		vprinter.PrinterName, vprinter.ListenAddr())
	return nil
}

func cmdRemovePrinter() error {
	if !vprinter.IsAdmin() {
		return fmt.Errorf("관리자 권한이 필요합니다. 관리자 PowerShell 에서 실행하세요")
	}
	if err := vprinter.Remove(); err != nil {
		return err
	}
	fmt.Printf("가상 프린터 제거 완료: %q\n", vprinter.PrinterName)
	return nil
}

func cmdInfo() error {
	e := resolveEngines()
	fmt.Print("탐지된 백엔드 엔진\n------------------\n")
	fmt.Print(e.String())
	return nil
}

func cmdPrinters(ctx context.Context) error {
	names, err := engine.ListPrinters(ctx)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Println("설치된 프린터가 없습니다.")
		return nil
	}
	fmt.Println("설치된 프린터")
	fmt.Println("-------------")
	for _, n := range names {
		fmt.Println(" -", n)
	}
	return nil
}

func cmdConvert(ctx context.Context, argv []string) error {
	fs := flag.NewFlagSet("convert", flag.ExitOnError)
	in := fs.String("i", "", "입력 파일 (PDF/PS/PCL/PXL)")
	out := fs.String("o", "", "출력 파일 (.pdf/.png/.jpg/.tif/.txt)")
	dpi := fs.Int("dpi", 300, "래스터 출력 해상도(DPI)")
	pages := fs.String("pages", "", "페이지 범위, 예) 1-3")
	verbose := fs.Bool("v", false, "상세 로그")
	fs.Parse(argv)

	if *in == "" || *out == "" {
		return fmt.Errorf("-i 입력과 -o 출력 경로가 필요합니다")
	}
	kind, err := engine.OutputKindFromExt(*out)
	if err != nil {
		return err
	}
	e := resolveEngines()
	return e.Convert(ctx, engine.ConvertOptions{
		Input: *in, Output: *out, Kind: kind,
		DPI: *dpi, Pages: *pages, Verbose: *verbose,
	})
}

func cmdPrint(ctx context.Context, argv []string) error {
	fs := flag.NewFlagSet("print", flag.ExitOnError)
	in := fs.String("i", "", "입력 파일 (PDF/PS/PCL/PXL)")
	printer := fs.String("p", "", "프린터 이름(생략 시 기본 프린터)")
	copies := fs.Int("copies", 1, "인쇄 부수")
	pages := fs.String("pages", "", "페이지 범위, 예) 1-3")
	verbose := fs.Bool("v", false, "상세 로그")
	fs.Parse(argv)

	if *in == "" {
		return fmt.Errorf("-i 입력 경로가 필요합니다")
	}
	e := resolveEngines()
	return e.Print(ctx, engine.PrintOptions{
		Input: *in, Printer: *printer,
		Copies: *copies, Pages: *pages, Verbose: *verbose,
	})
}

func usage() {
	fmt.Fprint(os.Stderr, `eprinter — 모두의프린터 핵심 엔진 클론 (Go, 단일 실행파일)

사용법:
  eprinter                  (인자 없이 실행하면 GUI 시작)
  eprinter <명령> [옵션]

명령:
  gui                       GUI 시작
  info                      탐지/임베드된 Ghostscript/GhostPCL 경로 표시
  printers                  설치된 프린터 목록 표시
  install-printer           가상 프린터 "모두의프린터" 설치 (관리자 권한)
  remove-printer            가상 프린터 제거 (관리자 권한)
  convert -i IN -o OUT      문서 변환 (형식 자동 감지)
      -dpi N                래스터(.png/.jpg/.tif) 해상도, 기본 300
      -pages 1-3            페이지 범위
      -v                    상세 로그
  print   -i IN [-p 이름]   프린터로 직접 출력
      -copies N             부수
      -pages 1-3            페이지 범위
      -v                    상세 로그

엔진은 exe 에 동봉되어 있어 별도 설치가 필요 없다.
시스템에 설치된 Ghostscript/GhostPCL 이 있으면 그것을 우선 사용하며,
환경변수 EPRINTER_GS / EPRINTER_GHOSTPCL 로 직접 지정할 수도 있다.

예시:
  eprinter convert -i report.pcl -o report.pdf
  eprinter convert -i report.pdf -o page.png -dpi 200 -pages 1-3
  eprinter print   -i report.pdf -p "Microsoft Print to PDF" -copies 2
`)
}
