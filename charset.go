package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// newCharsetReader decodes the XML encodings commonly used by podcast feeds.
// UTF-8 is handled by encoding/xml itself; this covers UTF-16 and single-byte
// Western encodings without introducing a large external dependency.
func newCharsetReader(label string, input io.Reader) (io.Reader, error) {
	data, err := io.ReadAll(input)
	if err != nil {
		return nil, err
	}
	normalized := strings.ToLower(strings.TrimSpace(label))
	switch normalized {
	case "utf-16", "utf-16le", "utf16le", "utf-16be", "utf16be", "unicode":
		littleEndian := true
		if normalized == "utf-16be" || normalized == "utf16be" {
			littleEndian = false
		} else if normalized == "utf-16" && bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
			littleEndian = false
		} else if normalized == "utf-16" && bytes.HasPrefix(data, []byte{0xff, 0xfe}) {
			littleEndian = true
		} else if normalized == "utf-16" && len(data) >= 2 && data[0] == 0 && data[1] == '<' {
			littleEndian = false
		}
		if (normalized == "utf-16le" || normalized == "utf16le") && bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
			return nil, fmt.Errorf("UTF-16LE declaration has a big-endian BOM")
		}
		if (normalized == "utf-16be" || normalized == "utf16be") && bytes.HasPrefix(data, []byte{0xff, 0xfe}) {
			return nil, fmt.Errorf("UTF-16BE declaration has a little-endian BOM")
		}
		if bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
			data = data[2:]
		}
		if len(data)%2 != 0 {
			return nil, fmt.Errorf("invalid UTF-16 input length")
		}
		units := make([]uint16, len(data)/2)
		for i := range units {
			if littleEndian {
				units[i] = uint16(data[i*2]) | uint16(data[i*2+1])<<8
			} else {
				units[i] = uint16(data[i*2])<<8 | uint16(data[i*2+1])
			}
		}
		return strings.NewReader(string(utf16.Decode(units))), nil
	case "windows-1252", "cp1252", "iso-8859-1", "iso8859-1", "latin1", "latin-1":
		return strings.NewReader(decodeWindows1252(data)), nil
	case "windows-1250", "cp1250":
		return strings.NewReader(decodeCodePage(data, codePage1250)), nil
	case "windows-1251", "cp1251":
		return strings.NewReader(decodeCodePage(data, codePage1251)), nil
	case "us-ascii", "ascii":
		for _, b := range data {
			if b >= 0x80 {
				return nil, fmt.Errorf("non-ASCII byte in ASCII document")
			}
		}
		return bytes.NewReader(data), nil
	case "utf-8", "utf8", "":
		return bytes.NewReader(data), nil
	default:
		return nil, fmt.Errorf("unsupported XML character encoding %q", label)
	}
}

func decodeCodePage(data []byte, table [128]rune) string {
	var out strings.Builder
	out.Grow(len(data))
	for _, b := range data {
		if b < 0x80 {
			out.WriteByte(b)
		} else {
			out.WriteRune(table[int(b)-0x80])
		}
	}
	return out.String()
}

var codePage1250 = [128]rune{
	'€', '�', '‚', '�', '„', '…', '†', '‡', '�', '‰', 'Š', '‹', 'Ś', 'Ť', 'Ž', 'Ź',
	'�', '‘', '’', '“', '”', '•', '–', '—', '�', '™', 'š', '›', 'ś', 'ť', 'ž', 'ź',
	' ', 'ˇ', '˘', 'Ł', '¤', 'Ą', '¦', '§', '¨', '©', 'Ş', '«', '¬', '­', '®', 'Ż',
	'°', '±', '˛', 'ł', '´', 'µ', '¶', '·', '¸', 'ą', 'ş', '»', 'Ľ', '˝', 'ľ', 'ż',
	'Ŕ', 'Á', 'Â', 'Ă', 'Ä', 'Ĺ', 'Ć', 'Ç', 'Č', 'É', 'Ę', 'Ë', 'Ě', 'Í', 'Î', 'Ď',
	'Đ', 'Ń', 'Ň', 'Ó', 'Ô', 'Ő', 'Ö', '×', 'Ř', 'Ů', 'Ú', 'Ű', 'Ü', 'Ý', 'Ţ', 'ß',
	'ŕ', 'á', 'â', 'ă', 'ä', 'ĺ', 'ć', 'ç', 'č', 'é', 'ę', 'ë', 'ě', 'í', 'î', 'ď',
	'đ', 'ń', 'ň', 'ó', 'ô', 'ő', 'ö', '÷', 'ř', 'ů', 'ú', 'ű', 'ü', 'ý', 'ţ', '˙',
}

var codePage1251 = [128]rune{
	'Ђ', 'Ѓ', '‚', 'ѓ', '„', '…', '†', '‡', '‰', '‰', 'Љ', '‹', 'Њ', 'Ќ', 'Ћ', 'Џ',
	'ђ', '‘', '’', '“', '”', '•', '–', '—', '™', '™', 'љ', '›', 'њ', 'ќ', 'ћ', 'џ',
	' ', 'Ў', 'ў', 'Ј', '¤', 'Ґ', '¦', '§', 'Ё', '©', 'Є', '«', '¬', '­', '®', 'Ї',
	'°', '±', 'І', 'і', 'ґ', 'µ', '¶', '·', 'ё', '№', 'є', '»', 'ј', 'Ѕ', 'ѕ', 'ї',
	'А', 'Б', 'В', 'Г', 'Д', 'Е', 'Ж', 'З', 'И', 'Й', 'К', 'Л', 'М', 'Н', 'О', 'П',
	'Р', 'С', 'Т', 'У', 'Ф', 'Х', 'Ц', 'Ч', 'Ш', 'Щ', 'Ъ', 'Ы', 'Ь', 'Э', 'Ю', 'Я',
	'а', 'б', 'в', 'г', 'д', 'е', 'ж', 'з', 'и', 'й', 'к', 'л', 'м', 'н', 'о', 'п',
	'р', 'с', 'т', 'у', 'ф', 'х', 'ц', 'ч', 'ш', 'щ', 'ъ', 'ы', 'ь', 'э', 'ю', 'я',
}

func decodeWindows1252(data []byte) string {
	const c1 = "€�‚ƒ„…†‡ˆ‰Š‹Œ�Ž��‘’“”•–—˜™š›œ�žŸ"
	var out strings.Builder
	out.Grow(len(data))
	for _, b := range data {
		if b >= 0x80 && b <= 0x9f {
			out.WriteRune([]rune(c1)[int(b-0x80)])
		} else {
			out.WriteRune(rune(b))
		}
	}
	return out.String()
}
