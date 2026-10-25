package authenticode

import (
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"strings"
	"unicode"
)

// This file answers one question: will electron-updater install an update that
// is signed by this signer, given the publisherName of app-update.yml? The
// answer is exact, not lenient like Matches: a build that passes a lenient
// check and fails this one installs fine and then refuses every update
// ("not signed by the application owner") on every client.
//
// The model follows electron-updater 6.8.9, windowsExecutableCodeSignatureVerifier.js:
//
//	subject = parseDn(Get-AuthenticodeSignature(file).SignerCertificate.Subject)
//	for each publisherName:
//	  dn = parseDn(publisherName)
//	  if dn is not empty: every key of dn has the same value in subject (===)
//	  else:               publisherName === subject.get("CN")
//
// so keys are case-sensitive, values are compared byte for byte, nothing is
// trimmed, and only the attributes that publisherName lists are compared.
// Windows PowerShell writes the signer's subject in its own form (windowsSubject),
// which is why a state is S= here and ST= nowhere. testdata/updater-publisher.json
// holds answers computed by the real code; app/scripts/publisher-name.test.mjs
// checks them again on every run of task app:ci.

// WindowsSubject is the subject of the signer as Windows PowerShell writes it:
// "CN=padmate, O=padmate contributors, S=Tokyo, C=JP". It is not the form of
// Subject, which is Go's.
func (s *Signature) WindowsSubject() string { return windowsSubject(s.Signer.RawSubject) }

// UpdaterAccepts says whether electron-updater accepts an update signed by this
// signer when app-update.yml names publisherName. See the comment of this file.
func (s *Signature) UpdaterAccepts(publisherName string) bool {
	return updaterAccepts(s.WindowsSubject(), publisherName)
}

func updaterAccepts(subject, publisherName string) bool {
	have := parseDN(subject)
	want := parseDN(publisherName)
	if len(want) > 0 {
		for key, value := range want {
			if got, ok := have[key]; !ok || got != value {
				return false
			}
		}
		return true
	}
	cn, ok := have["CN"]
	return ok && publisherName == cn
}

// windowsAttributeNames are the keywords Windows (CertNameToStr, the string of
// X509Certificate2.Subject) writes for the attribute types it names. Any other
// type is written as OID.1.2.3 (businessCategory, the Extended Validation
// jurisdiction attributes). The list is Windows' own: every type 2.5.4.0 to
// 2.5.4.90 and the others certificates use were asked of
// X500DistinguishedName.Name (testdata/windows-subjects.json has the printouts
// of the less common ones). An EV or OV certificate has a postal code, which
// Windows writes PostalCode=: a table without it would make verify print, and
// accept, a publisherName that electron-updater then refuses on every client.
var windowsAttributeNames = map[string]string{
	"2.5.4.3":                    "CN",
	"2.5.4.4":                    "SN",
	"2.5.4.5":                    "SERIALNUMBER",
	"2.5.4.6":                    "C",
	"2.5.4.7":                    "L",
	"2.5.4.8":                    "S",
	"2.5.4.9":                    "STREET",
	"2.5.4.10":                   "O",
	"2.5.4.11":                   "OU",
	"2.5.4.12":                   "T",
	"2.5.4.13":                   "Description",
	"2.5.4.17":                   "PostalCode",
	"2.5.4.18":                   "POBox",
	"2.5.4.20":                   "Phone",
	"2.5.4.24":                   "X21Address",
	"2.5.4.42":                   "G",
	"2.5.4.43":                   "I",
	"2.5.4.46":                   "dnQualifier",
	"2.23.133.2.1":               "TPMManufacturer",
	"2.23.133.2.2":               "TPMModel",
	"2.23.133.2.3":               "TPMVersion",
	"1.2.840.113549.1.9.1":       "E",
	"0.9.2342.19200300.100.1.25": "DC",
}

// windowsSubject writes a DER-encoded name the way Windows does: the most
// specific attribute first (the RDNs in reverse of the encoded order), ", "
// between RDNs, " + " between the attributes of a multi-valued RDN (which keep
// their encoded order: "OU=A + CN=Dev"), and a value in
// double quotes, with inner quotes doubled, when it is empty, begins or ends
// with a space, or contains one of ,+="<>#; or a line feed. testdata/windows-subjects.json
// is Windows' own output for these rules. An unreadable name gives "".
func windowsSubject(rawSubject []byte) string {
	var seq pkix.RDNSequence
	if rest, err := asn1.Unmarshal(rawSubject, &seq); err != nil || len(rest) != 0 {
		return ""
	}
	parts := make([]string, 0, len(seq))
	for i := len(seq) - 1; i >= 0; i-- {
		rdn := seq[i]
		attrs := make([]string, 0, len(rdn))
		for _, atv := range rdn {
			name, ok := windowsAttributeNames[atv.Type.String()]
			if !ok {
				name = "OID." + atv.Type.String()
			}
			attrs = append(attrs, name+"="+windowsValue(atv.Value))
		}
		parts = append(parts, strings.Join(attrs, " + "))
	}
	return strings.Join(parts, ", ")
}

func windowsValue(v any) string {
	s, ok := v.(string)
	if !ok {
		s = fmt.Sprint(v)
	}
	runes := []rune(s)
	if len(runes) == 0 ||
		unicode.IsSpace(runes[0]) || unicode.IsSpace(runes[len(runes)-1]) ||
		strings.ContainsAny(s, ",+=\"<>#;\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// parseDN is parseDn of builder-util-runtime (rfc2253Parser.js), which
// electron-updater uses on both sides of its comparison, ported statement for
// statement, quirks included: ',' ';' and '+' all separate attributes, a
// double quote only toggles quoting (so a doubled quote disappears), a later
// attribute of the same key replaces an earlier one, and a backslash followed
// by two hexadecimal digits is a character while a lone trailing backslash
// ends the parse before the last attribute is stored. JavaScript's parseInt
// for the two characters after a backslash is modeled in jsParseInt16.
func parseDN(seq string) map[string]string {
	runes := []rune(strings.TrimSpace(seq))
	result := map[string]string{}
	var (
		quoted       bool
		hasKey       bool
		key          string
		token        []rune
		nextNonSpace int
	)
	for i := 0; i <= len(runes); i++ {
		if i == len(runes) {
			if hasKey {
				result[key] = string(token)
			}
			break
		}
		ch := runes[i]
		if quoted {
			if ch == '"' {
				quoted = false
				continue
			}
		} else {
			if ch == '"' {
				quoted = true
				continue
			}
			if ch == '\\' {
				i++
				end := min(i+2, len(runes))
				var digits string
				if i < end {
					digits = string(runes[i:end])
				}
				if ord, ok := jsParseInt16(digits); ok {
					i++
					token = append(token, rune(uint16(ord)))
				} else if i < len(runes) {
					token = append(token, runes[i])
				}
				continue
			}
			if !hasKey && ch == '=' {
				key, hasKey = string(token), true
				token = nil
				continue
			}
			if ch == ',' || ch == ';' || ch == '+' {
				if hasKey {
					result[key] = string(token)
				}
				hasKey = false
				token = nil
				continue
			}
		}
		if ch == ' ' && !quoted {
			if len(token) == 0 {
				continue
			}
			if i > nextNonSpace {
				j := i
				for j < len(runes) && runes[j] == ' ' {
					j++
				}
				nextNonSpace = j
			}
			if nextNonSpace >= len(runes) {
				i = nextNonSpace - 1
				continue
			}
			if next := runes[nextNonSpace]; next == ',' || next == ';' ||
				(!hasKey && next == '=') || (hasKey && next == '+') {
				i = nextNonSpace - 1
				continue
			}
		}
		token = append(token, ch)
	}
	return result
}

// jsParseInt16 is parseInt(s, 16) for the one or two characters parseDn gives
// it: leading white space, a sign, a 0x prefix, then hexadecimal digits up to
// the first other character. ok is false for NaN.
func jsParseInt16(s string) (int, bool) {
	s = strings.TrimLeftFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == 0xFEFF })
	sign := 1
	if s != "" && (s[0] == '-' || s[0] == '+') {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		s = s[2:]
	}
	value, digits := 0, 0
	for _, c := range s {
		d := strings.IndexRune("0123456789abcdef", unicode.ToLower(c))
		if d < 0 {
			break
		}
		value, digits = value*16+d, digits+1
	}
	if digits == 0 {
		return 0, false
	}
	return sign * value, true
}
