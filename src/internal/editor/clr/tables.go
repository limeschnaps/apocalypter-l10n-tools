package clr

import (
	"encoding/binary"
	"fmt"
	"math/bits"
)

// Metadata table numbers (ECMA-335 II.22).
const (
	tableModule                 = 0x00
	tableTypeRef                = 0x01
	tableTypeDef                = 0x02
	tableFieldPtr               = 0x03
	tableField                  = 0x04
	tableMethodPtr              = 0x05
	tableMethodDef              = 0x06
	tableParamPtr               = 0x07
	tableParam                  = 0x08
	tableInterfaceImpl          = 0x09
	tableMemberRef              = 0x0a
	tableConstant               = 0x0b
	tableCustomAttribute        = 0x0c
	tableFieldMarshal           = 0x0d
	tableDeclSecurity           = 0x0e
	tableClassLayout            = 0x0f
	tableFieldLayout            = 0x10
	tableStandAloneSig          = 0x11
	tableEventMap               = 0x12
	tableEventPtr               = 0x13
	tableEvent                  = 0x14
	tablePropertyMap            = 0x15
	tablePropertyPtr            = 0x16
	tableProperty               = 0x17
	tableMethodSemantics        = 0x18
	tableMethodImpl             = 0x19
	tableModuleRef              = 0x1a
	tableTypeSpec               = 0x1b
	tableImplMap                = 0x1c
	tableFieldRVA               = 0x1d
	tableEncLog                 = 0x1e
	tableEncMap                 = 0x1f
	tableAssembly               = 0x20
	tableAssemblyProcessor      = 0x21
	tableAssemblyOS             = 0x22
	tableAssemblyRef            = 0x23
	tableAssemblyRefProcessor   = 0x24
	tableAssemblyRefOS          = 0x25
	tableFile                   = 0x26
	tableExportedType           = 0x27
	tableManifestResource       = 0x28
	tableNestedClass            = 0x29
	tableGenericParam           = 0x2a
	tableMethodSpec             = 0x2b
	tableGenericParamConstraint = 0x2c
	tableCount                  = 0x2d
)

// Column kinds. Values from colTable upward are simple indexes into the
// table colKind-colTable.
type colKind int

const (
	colU16 colKind = iota
	colU32
	colString
	colGUID
	colBlob
	// Coded indexes (II.24.2.6).
	colTypeDefOrRef
	colHasConstant
	colHasCustomAttribute
	colHasFieldMarshal
	colHasDeclSecurity
	colMemberRefParent
	colHasSemantics
	colMethodDefOrRef
	colMemberForwarded
	colImplementation
	colCustomAttributeType
	colResolutionScope
	colTypeOrMethodDef
	colTable
)

func index(table int) colKind { return colTable + colKind(table) }

// codedTables lists the tables of each coded index in tag order; -1 marks
// unused tags.
var codedTables = map[colKind][]int{
	colTypeDefOrRef: {tableTypeDef, tableTypeRef, tableTypeSpec},
	colHasConstant:  {tableField, tableParam, tableProperty},
	colHasCustomAttribute: {
		tableMethodDef, tableField, tableTypeRef, tableTypeDef, tableParam, tableInterfaceImpl, tableMemberRef,
		tableModule, tableDeclSecurity, tableProperty, tableEvent, tableStandAloneSig, tableModuleRef, tableTypeSpec,
		tableAssembly, tableAssemblyRef, tableFile, tableExportedType, tableManifestResource, tableGenericParam,
		tableGenericParamConstraint, tableMethodSpec,
	},
	colHasFieldMarshal:     {tableField, tableParam},
	colHasDeclSecurity:     {tableTypeDef, tableMethodDef, tableAssembly},
	colMemberRefParent:     {tableTypeDef, tableTypeRef, tableModuleRef, tableMethodDef, tableTypeSpec},
	colHasSemantics:        {tableEvent, tableProperty},
	colMethodDefOrRef:      {tableMethodDef, tableMemberRef},
	colMemberForwarded:     {tableField, tableMethodDef},
	colImplementation:      {tableFile, tableAssemblyRef, tableExportedType},
	colCustomAttributeType: {-1, -1, tableMethodDef, tableMemberRef, -1},
	colResolutionScope:     {tableModule, tableModuleRef, tableAssemblyRef, tableTypeRef},
	colTypeOrMethodDef:     {tableTypeDef, tableMethodDef},
}

// schema gives the columns of every table (II.22).
var schema = [tableCount][]colKind{
	tableModule:                 {colU16, colString, colGUID, colGUID, colGUID},
	tableTypeRef:                {colResolutionScope, colString, colString},
	tableTypeDef:                {colU32, colString, colString, colTypeDefOrRef, index(tableField), index(tableMethodDef)},
	tableFieldPtr:               {index(tableField)},
	tableField:                  {colU16, colString, colBlob},
	tableMethodPtr:              {index(tableMethodDef)},
	tableMethodDef:              {colU32, colU16, colU16, colString, colBlob, index(tableParam)},
	tableParamPtr:               {index(tableParam)},
	tableParam:                  {colU16, colU16, colString},
	tableInterfaceImpl:          {index(tableTypeDef), colTypeDefOrRef},
	tableMemberRef:              {colMemberRefParent, colString, colBlob},
	tableConstant:               {colU16, colHasConstant, colBlob},
	tableCustomAttribute:        {colHasCustomAttribute, colCustomAttributeType, colBlob},
	tableFieldMarshal:           {colHasFieldMarshal, colBlob},
	tableDeclSecurity:           {colU16, colHasDeclSecurity, colBlob},
	tableClassLayout:            {colU16, colU32, index(tableTypeDef)},
	tableFieldLayout:            {colU32, index(tableField)},
	tableStandAloneSig:          {colBlob},
	tableEventMap:               {index(tableTypeDef), index(tableEvent)},
	tableEventPtr:               {index(tableEvent)},
	tableEvent:                  {colU16, colString, colTypeDefOrRef},
	tablePropertyMap:            {index(tableTypeDef), index(tableProperty)},
	tablePropertyPtr:            {index(tableProperty)},
	tableProperty:               {colU16, colString, colBlob},
	tableMethodSemantics:        {colU16, index(tableMethodDef), colHasSemantics},
	tableMethodImpl:             {index(tableTypeDef), colMethodDefOrRef, colMethodDefOrRef},
	tableModuleRef:              {colString},
	tableTypeSpec:               {colBlob},
	tableImplMap:                {colU16, colMemberForwarded, colString, index(tableModuleRef)},
	tableFieldRVA:               {colU32, index(tableField)},
	tableEncLog:                 {colU32, colU32},
	tableEncMap:                 {colU32},
	tableAssembly:               {colU32, colU16, colU16, colU16, colU16, colU32, colBlob, colString, colString},
	tableAssemblyProcessor:      {colU32},
	tableAssemblyOS:             {colU32, colU32, colU32},
	tableAssemblyRef:            {colU16, colU16, colU16, colU16, colU32, colBlob, colString, colString, colBlob},
	tableAssemblyRefProcessor:   {colU32, index(tableAssemblyRef)},
	tableAssemblyRefOS:          {colU32, colU32, colU32, index(tableAssemblyRef)},
	tableFile:                   {colU32, colString, colBlob},
	tableExportedType:           {colU32, colU32, colString, colString, colImplementation},
	tableManifestResource:       {colU32, colU32, colString, colImplementation},
	tableNestedClass:            {index(tableTypeDef), index(tableTypeDef)},
	tableGenericParam:           {colU16, colU16, colTypeOrMethodDef, colString},
	tableMethodSpec:             {colMethodDefOrRef, colBlob},
	tableGenericParamConstraint: {index(tableGenericParam), colTypeDefOrRef},
}

// table is one decoded table stream section.
type table struct {
	rows    int
	rowSize int
	offsets []int
	sizes   []int
	data    []byte
}

// value returns column col of row (1-based).
func (t *table) value(row, col int) uint32 {
	p := (row-1)*t.rowSize + t.offsets[col]
	if t.sizes[col] == 2 {
		return uint32(binary.LittleEndian.Uint16(t.data[p:]))
	}
	return binary.LittleEndian.Uint32(t.data[p:])
}

// tables holds every table of the #~ stream.
type tables [tableCount]table

// Heap size flags of the #~ header.
const (
	heapStringsWide = 0x01
	heapGUIDWide    = 0x02
	heapBlobWide    = 0x04
	heapExtraData   = 0x40
)

// parseTables decodes the #~ (or #-) stream.
func parseTables(data []byte) (*tables, error) {
	if len(data) < 24 {
		return nil, fmt.Errorf("%w: table stream too short", ErrFormat)
	}
	heapSizes := data[6]
	valid := binary.LittleEndian.Uint64(data[8:])
	pos := 24
	var rows [64]int
	for i := range 64 {
		if valid&(1<<i) == 0 {
			continue
		}
		if pos+4 > len(data) {
			return nil, fmt.Errorf("%w: truncated row counts", ErrFormat)
		}
		rows[i] = int(binary.LittleEndian.Uint32(data[pos:]))
		pos += 4
		if i >= tableCount && rows[i] > 0 {
			return nil, fmt.Errorf("%w: unknown table 0x%x", ErrUnsupported, i)
		}
	}
	if heapSizes&heapExtraData != 0 {
		pos += 4
	}

	size := func(k colKind) int {
		switch {
		case k == colU16:
			return 2
		case k == colU32:
			return 4
		case k == colString:
			return wide(heapSizes&heapStringsWide != 0)
		case k == colGUID:
			return wide(heapSizes&heapGUIDWide != 0)
		case k == colBlob:
			return wide(heapSizes&heapBlobWide != 0)
		case k >= colTable:
			return wide(rows[k-colTable] >= 1<<16)
		}
		targets := codedTables[k]
		tagBits := bits.Len(uint(len(targets) - 1))
		maxRows := 0
		for _, t := range targets {
			if t >= 0 {
				maxRows = max(maxRows, rows[t])
			}
		}
		return wide(maxRows >= 1<<(16-tagBits))
	}

	ts := &tables{}
	for i := range tableCount {
		t := &ts[i]
		t.rows = rows[i]
		for _, k := range schema[i] {
			t.offsets = append(t.offsets, t.rowSize)
			s := size(k)
			t.sizes = append(t.sizes, s)
			t.rowSize += s
		}
		n := t.rows * t.rowSize
		if n < 0 || pos+n > len(data) {
			return nil, fmt.Errorf("%w: table 0x%x truncated", ErrFormat, i)
		}
		t.data = data[pos : pos+n]
		pos += n
	}
	if ts[tableFieldPtr].rows > 0 || ts[tableMethodPtr].rows > 0 {
		return nil, fmt.Errorf("%w: uncompressed metadata with pointer tables", ErrUnsupported)
	}
	return ts, nil
}

func wide(b bool) int {
	if b {
		return 4
	}
	return 2
}

// decodeCoded splits a coded index into a token.
func decodeCoded(k colKind, v uint32) Token {
	targets := codedTables[k]
	tagBits := bits.Len(uint(len(targets) - 1))
	tag := int(v & (1<<tagBits - 1))
	row := v >> tagBits
	if tag >= len(targets) || targets[tag] < 0 || row == 0 {
		return 0
	}
	return NewToken(targets[tag], int(row))
}
