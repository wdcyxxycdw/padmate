package installerpkg

import (
	"bytes"
	"strings"
)

// The two languages of padmate's window, as its language file names them.
const (
	LanguageChinese = "zh-CN"
	LanguageEnglish = "en"
)

// LanguageFile is the file of the window's language, in its browser data
// (%APPDATA%\padmate\ui). The settings page writes it when the person picks a
// language (app/src/main/ui-language.ts), and the installer writes the
// language chosen in its first dialog unless the person has picked one
// (app/build/padmate-options.nsh, ADR-0018).
const LanguageFile = "language.json"

// InstallerLanguage is the language the installer offers first for a Windows
// display language id (GetUserDefaultUILanguage): Chinese for every Chinese,
// English for the rest. PadmateLanguageFor does it in NSIS.
func InstallerLanguage(langID uint16) string {
	if langID&0x3FF == 0x04 {
		return LanguageChinese
	}
	return LanguageEnglish
}

// LanguageLine is what the installer writes into LanguageFile for a language:
// the same JSON the settings page writes, with setBy "installer".
func LanguageLine(language string) string {
	return `{"language":"` + language + `","setBy":"installer"}` + "\n"
}

// WritesLanguage reports whether the installer writes LanguageFile over what
// it holds now (exists false when there is no file): when there is none, or
// when its first line is one the installer wrote. Anything else, a choice made
// on the settings page above all, is kept. PadmateWriteLanguage does it in NSIS.
func WritesLanguage(old []byte, exists bool) bool {
	if !exists {
		return true
	}
	line, _, _ := bytes.Cut(old, []byte("\n"))
	first := strings.TrimRight(string(line), "\r")
	for _, language := range []string{LanguageChinese, LanguageEnglish} {
		if first == strings.TrimSuffix(LanguageLine(language), "\n") {
			return true
		}
	}
	return false
}

// DesktopShortcutText is the text of the wizard's box for the desktop
// shortcut, in Chinese and in English (installer.nsh); tests know the page by
// it.
var DesktopShortcutText = [2]string{"在桌面创建快捷方式", "Create a shortcut on the desktop"}

// DesktopDefault is whether the box "create a shortcut on the desktop" starts
// ticked: on a first installation (none recorded) it does, and over an
// installation it does when the desktop has the shortcut now, so that a
// shortcut the person deleted does not come back. PadmateDesktopDefault does it
// in NSIS; --no-desktop-shortcut unticks it in any case.
func DesktopDefault(installed, linkExists bool) bool {
	return !installed || linkExists
}
