# eprinter — 모두의프린터 클론 (Go, 단일 실행파일)

원본 `ebp349_x64.exe`(모두의프린터, Go 작성)의 **핵심 변환/출력 엔진**을 재구현한 것.
CLI 와 GUI 를 **하나의 exe** 로 제공하며, 백엔드 엔진(Ghostscript/GhostPCL)까지
실행파일 안에 임베드되어 **별도 설치 없이 exe 하나로 동작**한다.

- 인자 없이 실행(더블클릭) → **GUI**
- 인자와 함께 실행 → **CLI**

## 동작 구조

원본과 동일하게 **Ghostscript / GhostPCL** 을 백엔드 렌더링 엔진으로 호출한다.

```
입력파일 ─▶ 형식 자동감지 ─▶ 엔진 선택 ─▶ Ghostscript/GhostPCL 실행 ─▶ 변환물 / 프린터 출력
           (PDF/PS/PCL/PXL)   (gs / gpcl6)
```

| 입력 형식 | 사용 엔진 |
|-----------|-----------|
| PDF, PostScript | Ghostscript (`gswin64c.exe`) |
| PCL, PCL-XL | GhostPCL (`gpcl6win64.exe`) |

엔진 선택 우선순위: **환경변수(`EPRINTER_GS`/`EPRINTER_GHOSTPCL`) → 시스템 설치 →
임베드 번들**. 시스템에 깔려 있으면 그걸 쓰고, 없으면 exe 에 동봉된 엔진을 사용자
캐시(`%LOCALAPPDATA%\eprinter\...`)로 첫 실행 시 한 번 풀어서 쓴다.

## 단일 실행파일 빌드

Go 1.21+ 필요.

```powershell
cd eprinter-go

# 1) 매니페스트(walk 공통컨트롤 v6 + DPI)를 COFF 리소스(.syso)로 변환해 임베드
#    (외부 rsrc 도구 불필요 — 동봉한 tools/mkrsrc 생성기 사용)
go run ./tools/mkrsrc .\eprinter.manifest .\rsrc_windows_amd64.syso

# 2) 단일 exe 빌드 (콘솔창 없는 windowsgui 서브시스템)
go build -ldflags "-H windowsgui" -o eprinter.exe .

go test ./...   # 형식감지 등 단위 테스트
```

결과물은 **`eprinter.exe` 한 개**(약 40MB). 이 파일만 복사하면 어디서든 동작한다.

### 임베드 엔진 갱신
`internal/bundle/engines.zip` 에 엔진 일체가 들어 있다. 구성:

```
engines/gs/bin/{gswin64c.exe, gsdll64.dll}, gs/lib, gs/Resource, gs/iccprofiles
engines/pcl/{gpcl6win64.exe, gpcl6dll64.dll}
```

엔진 버전을 바꾸려면 이 zip 을 교체하고 `internal/bundle/bundle.go` 의
`bundleVersion` 상수를 올린 뒤(캐시 재추출 유도) 다시 빌드한다.
> zip 은 반드시 정규(forward-slash) 형식으로 — PowerShell `Compress-Archive`(PS5.1)는
> 역슬래시 경로 버그가 있으니 7-Zip 등으로 만들 것. (추출기는 두 경우 모두 처리한다.)

## 가상 프린터 "eprinter"

다른 프로그램(한글/브라우저 등)에서 **"eprinter"로 인쇄하면 자동으로 PDF로 저장**된다.
(프린터 이름은 일부 드라이버/대화상자의 인코딩 문제로 ASCII `eprinter` 를 쓴다.)

동작 원리(RedMon 등 외부 의존성 0):
1. PostScript 드라이버 + **로컬 파일 포트**를 가진 프린터를 설치 → 인쇄 데이터(PostScript)가
   포트 파일(`%ProgramData%\eprinter\spool\eprinter.ps`)로 스풀된다.
2. GUI 가 그 폴더를 감시하다가 새 작업이 떨어지면 임베드 Ghostscript 로 PDF 변환 →
   **자동 저장 폴더**(기본: 바탕화면)에 `eprinter_<날짜시각>.pdf` 로 저장한다.

라이프사이클(**a안**): **GUI 실행 시 설치 / 종료 시 자동 삭제**. 설치·삭제에는 관리자
권한이 필요하므로, GUI 를 권한 없이 실행하면 UAC 로 자기 자신을 재실행한다.

> 스풀 폴더는 반드시 `%ProgramData%` 등 **시스템 공용 경로**여야 한다. 인쇄 스풀러는
> SYSTEM 권한으로 돌며, 보안 강화(PrintNightmare 대응)로 **사용자 프로필(`%LOCALAPPDATA%`)
> 경로의 로컬 포트 생성을 거부**한다(`0x8007007b`).
>
> PostScript 드라이버는 설치된 것 중 이름에 `PostScript`/`PS` 가 든 것을 자동 선택한다.
> 강제 지정하려면 환경변수 `EPRINTER_PRINTER_DRIVER` 에 드라이버 이름을 넣는다.

설치/삭제는 CLI 로도 가능(관리자 권한 필요):

```powershell
eprinter install-printer    # 가상 프린터 설치
eprinter remove-printer     # 가상 프린터 제거
```

## GUI

walk 기반. 실행 시:
- **가상 프린터 상태** + **자동 저장 폴더** 선택 + **변환 기록** 로그
- **백엔드 엔진** 경로 표시(임베드/시스템)
- **프린터** 자동 인식(콤보박스) + 새로고침
- **입력 문서** 선택(찾아보기)
- **PDF 저장 위치/파일명** 지정(저장 대화상자, 입력명 기반 자동 제안)
- **[PDF로 변환]**, **[선택한 프린터로 인쇄]**

## 사용법 (CLI)

```powershell
eprinter                                        # (인자 없음) GUI 시작
eprinter info                                   # 임베드/탐지된 엔진 경로 확인
eprinter printers                               # 설치된 프린터 목록
eprinter install-printer                         # 가상 프린터 "eprinter" 설치(관리자)
eprinter remove-printer                          # 가상 프린터 제거(관리자)

# 변환 (형식 자동 감지)
eprinter convert -i report.pcl -o report.pdf
eprinter convert -i report.pdf -o page.png -dpi 200 -pages 1-3
eprinter convert -i report.ps  -o text.txt      # 텍스트 추출

# 프린터로 직접 출력 (mswinpr2 device)
eprinter print -i report.pdf -p "Microsoft Print to PDF" -copies 2
eprinter print -i report.pcl                     # 기본 프린터로
```

## 원본 대비 범위

| 기능 | 원본(ebp349) | 이 클론 |
|------|:---:|:---:|
| PDF/PS/PCL/PXL 변환 | ✅ | ✅ |
| 프린터 직접 출력 | ✅ | ✅ |
| 형식 자동 감지 | ✅ | ✅ |
| GUI (walk) | ✅ | ✅ |
| 엔진 임베드(단일 exe) | ✅ | ✅ |
| 가상 프린터 설치 → 인쇄 자동 PDF | ✅ | ✅ |
| 자동 업데이트/네트워크 | ✅ | ❌ |

## 프로젝트 구조

```
eprinter-go/
├── main.go               # 디스패처 (인자 유무로 CLI/GUI 분기) + 엔진 결정
├── cli.go                # CLI 서브커맨드 (info/printers/convert/print/install-printer/remove-printer)
├── gui.go                # walk GUI + 가상 프린터 라이프사이클(a안)
├── console_windows.go    # windowsgui 에서 콘솔 출력 연결(AttachConsole)
├── console_other.go      # 비-Windows no-op
├── eprinter.manifest     # 매니페스트 소스 (→ .syso 로 임베드)
├── rsrc_windows_amd64.syso  # 임베드된 매니페스트 리소스 (생성물)
├── internal/
│   ├── engine/           # 엔진 탐색/형식감지/변환/인쇄
│   ├── vprinter/         # 가상 프린터 설치·제거(관리자/UAC) + 스풀 폴더 감시
│   └── bundle/           # 엔진 임베드(engines.zip) + 첫 실행 시 추출
└── tools/mkrsrc/         # 매니페스트→.syso 변환 생성기 (외부 의존성 0)
```

## 라이선스 주의

백엔드인 Ghostscript는 **AGPL**, GhostPCL은 **AFPL** 이다.
임베드해 배포할 경우 상업적 사용 시 Artifex 상용 라이선스가 필요할 수 있다.
```
