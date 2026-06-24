// mkrsrc — 애플리케이션 매니페스트를 Windows/amd64 COFF 오브젝트(.syso)로 변환한다.
//
// go build 는 패키지 디렉터리의 *.syso 를 자동으로 링크하므로, 이렇게 만든
// rsrc_windows_amd64.syso 를 두면 매니페스트가 exe 안에 임베드된다(외부 도구 불필요).
//
// 단일 RT_MANIFEST 리소스(타입 24, ID 1, 언어 중립)만 담는 최소 구현.
//
// 사용: go run ./tools/mkrsrc <manifest.in> <out.syso>
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "사용법: mkrsrc <manifest> <out.syso>")
		os.Exit(2)
	}
	manifest, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(os.Args[2], buildSyso(manifest), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("생성: %s (manifest %d bytes)\n", os.Args[2], len(manifest))
}

func fail(err error) { fmt.Fprintln(os.Stderr, "오류:", err); os.Exit(1) }

const (
	imageFileMachineAMD64 = 0x8664
	scnInitReadData       = 0x40000040 // CNT_INITIALIZED_DATA | MEM_READ
	relAMD64ADDR32NB      = 0x0003
	symClassStatic        = 3
	rtManifest            = 24
)

// .rsrc 섹션 내부 레이아웃(섹션 시작 기준 오프셋):
//
//	0  : L1 디렉터리(타입)      16 + 엔트리 8  = 24
//	24 : L2 디렉터리(이름/ID)   16 + 엔트리 8  = 48
//	48 : L3 디렉터리(언어)      16 + 엔트리 8  = 72
//	72 : DATA_ENTRY            16            = 88
//	88 : 매니페스트 바이트
const (
	offL2       = 24
	offL3       = 48
	offDataEnt  = 72
	offRawBytes = 88
)

func buildSyso(manifest []byte) []byte {
	// --- .rsrc 섹션 데이터 구성 ---
	var rsrc bytes.Buffer
	writeDir(&rsrc, rtManifest, 0x80000000|offL2) // L1: type=RT_MANIFEST → L2
	writeDir(&rsrc, 1, 0x80000000|offL3)          // L2: id=1            → L3
	writeDir(&rsrc, 0, offDataEnt)                // L3: lang=neutral    → DATA_ENTRY (leaf)
	// IMAGE_RESOURCE_DATA_ENTRY
	binary.Write(&rsrc, binary.LittleEndian, uint32(offRawBytes)) // OffsetToData(RVA) — 재배치 대상
	binary.Write(&rsrc, binary.LittleEndian, uint32(len(manifest)))
	binary.Write(&rsrc, binary.LittleEndian, uint32(0)) // CodePage
	binary.Write(&rsrc, binary.LittleEndian, uint32(0)) // Reserved
	rsrc.Write(manifest)
	for rsrc.Len()%4 != 0 { // 4바이트 정렬
		rsrc.WriteByte(0)
	}
	rsrcData := rsrc.Bytes()

	// --- 파일 오프셋 계산 ---
	const fileHdr = 20
	const secHdr = 40
	ptrRaw := fileHdr + secHdr
	ptrReloc := ptrRaw + len(rsrcData)
	ptrSym := ptrReloc + 10 // 재배치 1개(10바이트)

	var out bytes.Buffer
	// IMAGE_FILE_HEADER
	binary.Write(&out, binary.LittleEndian, uint16(imageFileMachineAMD64))
	binary.Write(&out, binary.LittleEndian, uint16(1)) // NumberOfSections
	binary.Write(&out, binary.LittleEndian, uint32(0)) // TimeDateStamp
	binary.Write(&out, binary.LittleEndian, uint32(ptrSym))
	binary.Write(&out, binary.LittleEndian, uint32(1)) // NumberOfSymbols
	binary.Write(&out, binary.LittleEndian, uint16(0)) // SizeOfOptionalHeader
	binary.Write(&out, binary.LittleEndian, uint16(0)) // Characteristics

	// Section header (.rsrc)
	out.Write(secName(".rsrc"))
	binary.Write(&out, binary.LittleEndian, uint32(0)) // VirtualSize
	binary.Write(&out, binary.LittleEndian, uint32(0)) // VirtualAddress
	binary.Write(&out, binary.LittleEndian, uint32(len(rsrcData)))
	binary.Write(&out, binary.LittleEndian, uint32(ptrRaw))
	binary.Write(&out, binary.LittleEndian, uint32(ptrReloc))
	binary.Write(&out, binary.LittleEndian, uint32(0)) // PointerToLinenumbers
	binary.Write(&out, binary.LittleEndian, uint16(1)) // NumberOfRelocations
	binary.Write(&out, binary.LittleEndian, uint16(0)) // NumberOfLinenumbers
	binary.Write(&out, binary.LittleEndian, uint32(scnInitReadData))

	// .rsrc raw data
	out.Write(rsrcData)

	// Relocation: DATA_ENTRY.OffsetToData(파일 내 offDataEnt) 를 섹션 심볼 기준 RVA 로 보정
	binary.Write(&out, binary.LittleEndian, uint32(offDataEnt)) // VirtualAddress
	binary.Write(&out, binary.LittleEndian, uint32(0))          // SymbolTableIndex(=0, .rsrc 심볼)
	binary.Write(&out, binary.LittleEndian, uint16(relAMD64ADDR32NB))

	// Symbol table: .rsrc 섹션 심볼 1개
	out.Write(secName(".rsrc"))
	binary.Write(&out, binary.LittleEndian, uint32(0)) // Value
	binary.Write(&out, binary.LittleEndian, uint16(1)) // SectionNumber(1-based)
	binary.Write(&out, binary.LittleEndian, uint16(0)) // Type
	out.WriteByte(symClassStatic)                      // StorageClass
	out.WriteByte(0)                                   // NumberOfAuxSymbols

	// String table: 길이 필드만(4) — 긴 이름 없음
	binary.Write(&out, binary.LittleEndian, uint32(4))

	return out.Bytes()
}

// IMAGE_RESOURCE_DIRECTORY(16) + 엔트리 1개(8) 를 쓴다.
func writeDir(b *bytes.Buffer, id, offset uint32) {
	binary.Write(b, binary.LittleEndian, uint32(0)) // Characteristics
	binary.Write(b, binary.LittleEndian, uint32(0)) // TimeDateStamp
	binary.Write(b, binary.LittleEndian, uint16(0)) // MajorVersion
	binary.Write(b, binary.LittleEndian, uint16(0)) // MinorVersion
	binary.Write(b, binary.LittleEndian, uint16(0)) // NumberOfNamedEntries
	binary.Write(b, binary.LittleEndian, uint16(1)) // NumberOfIdEntries
	binary.Write(b, binary.LittleEndian, id)        // 엔트리: Name/Id
	binary.Write(b, binary.LittleEndian, offset)    // 엔트리: OffsetToData
}

// 8바이트 섹션/심볼 이름(널 패딩).
func secName(s string) []byte {
	n := make([]byte, 8)
	copy(n, s)
	return n
}
