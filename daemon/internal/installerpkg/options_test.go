package installerpkg

import "testing"

// languageIDCases are Windows display language ids and the language the
// installer offers first for each (TestOptionFunctionsNSIS runs them too).
var languageIDCases = []struct {
	id   uint16
	want string
}{
	{0x0804, LanguageChinese}, // zh-CN
	{0x0404, LanguageChinese}, // zh-TW
	{0x0C04, LanguageChinese}, // zh-HK
	{0x1004, LanguageChinese}, // zh-SG
	{0x0409, LanguageEnglish}, // en-US
	{0x0809, LanguageEnglish}, // en-GB
	{0x0407, LanguageEnglish}, // de-DE
	{0x0411, LanguageEnglish}, // ja-JP
	{0x0000, LanguageEnglish},
}

// languageFileCases are what LanguageFile may hold when the installer comes,
// and whether it writes its own line over that (TestOptionFunctionsNSIS).
var languageFileCases = []struct {
	name   string
	old    string
	exists bool
	writes bool
}{
	{"no file", "", false, true},
	{"the installer's Chinese", `{"language":"zh-CN","setBy":"installer"}` + "\n", true, true},
	{"the installer's English", `{"language":"en","setBy":"installer"}` + "\n", true, true},
	{"the installer's, with CRLF", `{"language":"en","setBy":"installer"}` + "\r\n", true, true},
	{"the installer's, without an end of line", `{"language":"zh-CN","setBy":"installer"}`, true, true},
	{"chosen in the settings", `{"language":"zh-CN","setBy":"settings"}` + "\n", true, false},
	{"chosen in the settings, English", `{"language":"en","setBy":"settings"}` + "\n", true, false},
	{"written by hand, with spaces", `{"language": "en", "setBy": "installer"}` + "\n", true, false},
	{"another case", `{"language":"EN","setBy":"installer"}` + "\n", true, false},
	{"an empty file", "", true, false},
	{"not JSON", "garbage\n", true, false},
	{"over several lines", "{\n  \"language\": \"en\"\n}\n", true, false},
}

func TestInstallerLanguage(t *testing.T) {
	for _, c := range languageIDCases {
		if got := InstallerLanguage(c.id); got != c.want {
			t.Errorf("InstallerLanguage(%#04x) = %q, want %q", c.id, got, c.want)
		}
	}
}

func TestWritesLanguage(t *testing.T) {
	for _, c := range languageFileCases {
		if got := WritesLanguage([]byte(c.old), c.exists); got != c.writes {
			t.Errorf("%s: WritesLanguage(%q, %v) = %v, want %v", c.name, c.old, c.exists, got, c.writes)
		}
	}
	// What the installer writes, it may write again: a later install in the
	// other language changes a language nobody picked in the settings.
	for _, language := range []string{LanguageChinese, LanguageEnglish} {
		if !WritesLanguage([]byte(LanguageLine(language)), true) {
			t.Errorf("the installer would keep its own line %q", LanguageLine(language))
		}
	}
}

func TestDesktopDefault(t *testing.T) {
	for _, c := range []struct{ installed, exists, want bool }{
		{false, false, true}, {false, true, true}, {true, true, true}, {true, false, false},
	} {
		if got := DesktopDefault(c.installed, c.exists); got != c.want {
			t.Errorf("DesktopDefault(installed %v, link %v) = %v, want %v", c.installed, c.exists, got, c.want)
		}
	}
}
