package po

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// poeditFile resembles what Poedit writes after a translator worked on a
// generated file.
const poeditFile = `# Translator notes for the file.
msgid ""
msgstr ""
"Content-Type: text/plain; charset=UTF-8\n"
"Language: ru\n"

#. level0 | Text | m_Text
#: level0
msgid "Duke Ironjaw"
msgstr "Дюк Железнозуб"

# check the length
#, fuzzy, c-format
#| msgid "Old text"
msgid "Nuts"
msgstr "Гайки"

msgid ""
"First line\n"
"Second \"quoted\" line"
msgstr ""
"Первая строка\n"
"Вторая «строка»"

msgid "Tab\there \\ \a\b\f\v\r \101\x42 \?"
msgstr ""

#~ msgid "Gone"
#~ msgstr "Пропало"
#~| msgid "Older"
`

func TestParse(t *testing.T) {
	f, err := Parse([]byte(poeditFile))
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := Message{Comments: []string{"Translator notes for the file."}, Str: "Content-Type: text/plain; charset=UTF-8\nLanguage: ru\n"}
	if !reflect.DeepEqual(f.Header, wantHeader) {
		t.Errorf("header = %+v", f.Header)
	}
	want := []Message{
		{Extracted: []string{"level0 | Text | m_Text"}, References: []string{"level0"}, ID: "Duke Ironjaw", Str: "Дюк Железнозуб"},
		{Comments: []string{"check the length"}, Flags: []string{"fuzzy", "c-format"}, ID: "Nuts", Str: "Гайки"},
		{ID: "First line\nSecond \"quoted\" line", Str: "Первая строка\nВторая «строка»"},
		{ID: "Tab\there \\ \a\b\f\v\r AB ?"},
		{ID: "Gone", Str: "Пропало", Obsolete: true},
	}
	if !reflect.DeepEqual(f.Messages, want) {
		t.Errorf("messages:\n%+v\nwant\n%+v", f.Messages, want)
	}
	if !f.Messages[1].Fuzzy() || f.Messages[0].Fuzzy() {
		t.Error("fuzzy flag")
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	f, err := Parse([]byte(poeditFile))
	if err != nil {
		t.Fatal(err)
	}
	f.Messages = append(f.Messages, Message{Context: "menu", ID: "ctl\x01", Str: "del\x7f"})
	data := Marshal(f)
	again, err := Parse(data)
	if err != nil {
		t.Fatalf("parse marshaled:\n%s\n%v", data, err)
	}
	if !reflect.DeepEqual(again, f) {
		t.Errorf("round trip:\n%+v\nwant\n%+v", again, f)
	}
	for _, s := range []string{
		"msgid \"\"\n\"First line\\n\"\n\"Second \\\"quoted\\\" line\"\n",
		"#~ msgid \"Gone\"\n#~ msgstr \"Пропало\"\n",
		"#, fuzzy, c-format\n",
		`msgid "ctl\001"`,
	} {
		if !strings.Contains(string(data), s) {
			t.Errorf("output lacks %q:\n%s", s, data)
		}
	}
}

func TestParseEmpty(t *testing.T) {
	f, err := Parse(nil)
	if err != nil || len(f.Messages) != 0 || f.Header.Str != "" {
		t.Errorf("empty file = %+v, %v", f, err)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"plural":             "msgid \"a\"\nmsgid_plural \"b\"\nmsgstr[0] \"x\"\n",
		"no msgstr":          "msgid \"a\"\n\nmsgid \"b\"\nmsgstr \"\"\n",
		"no msgstr at end":   "msgid \"a\"\n",
		"msgstr first":       "msgstr \"a\"\n",
		"msgid twice":        "msgid \"a\"\nmsgid \"b\"\nmsgstr \"\"\n",
		"unknown keyword":    "msgfoo \"a\"\n",
		"unquoted":           "msgid a\nmsgstr \"\"\n",
		"bare string":        "\"a\"\n",
		"inner quote":        "msgid \"a\"b\"\nmsgstr \"\"\n",
		"trailing backslash": "msgid \"a\\\"\nmsgstr \"\"\n",
		"unknown escape":     "msgid \"\\q\"\nmsgstr \"\"\n",
		"empty hex":          "msgid \"\\xg\"\nmsgstr \"\"\n",
		"octal overflow":     "msgid \"\\777\"\nmsgstr \"\"\n",
		"late header":        "msgid \"a\"\nmsgstr \"\"\n\nmsgid \"\"\nmsgstr \"h\"\n",
		"stray comments":     "# lonely\n",
	}
	for name, data := range cases {
		if _, err := Parse([]byte(data)); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
