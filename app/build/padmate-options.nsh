; The choices of padmate's installation wizard besides the folder (ADR-0018):
; the language, asked in a dialog before the wizard, and the desktop shortcut,
; a box on a page of its own after the folder page. Apart from installer.nsh
; so that tests can compile and run it by themselves
; (daemon/internal/installerpkg/options_nsis_windows_test.go runs the rules,
; daemon/internal/installersmoke/optionspage_windows_test.go the dialog and the
; page). The Go functions InstallerLanguage, WritesLanguage and DesktopDefault
; of installerpkg do what the rules do, and tests keep them equal.
;
; The language is that of the wizard's text and the first language of
; padmate's window: the installer writes it into the window's language file
; (language.json in its browser data), in the form the settings page writes
; (app/src/main/ui-language.ts), unless the person has picked a language on
; that page. A file the installer wrote is its own to write again.
;
; This file is ASCII only and defines two macros: PadmateOptionsPage, the page
; (lay it out where the pages are, before the functions), and
; PadmateOptionFunctions, the functions. The caller has
;   - included MUI2, LogicLib, nsDialogs and FileFunc, and inserted the
;     MUI_LANGUAGE of SimpChinese and English,
;   - defined PADMATE_LANGDLL_TITLE and PADMATE_LANGDLL_TEXT (the dialog's
;     title and text, in both languages, as nobody has chosen yet),
;     PADMATE_DESKTOP_LINK (the path of the desktop shortcut) and
;     INSTALL_REGISTRY_KEY, and the conditions ${isUpdated} and
;     ${isNoDesktopShortcut},
;   - defined the LangStrings padmateOptionsTitle and padmateOptionsText (the
;     page's header) and padmateDesktopShortcut (its box).
; Expand each once, and only in the installer.
;
; The functions take and give their data in registers or on the stack, as noted
; on each, and leave every other register as they found it.

; The two languages of the installer (LANG_SIMPCHINESE, LANG_ENGLISH).
!define PADMATE_LANG_ZH 2052
!define PADMATE_LANG_EN 1033

; The window's language file, in its browser data (installerpkg.LanguageFile).
!define PADMATE_LANGUAGE_FILE "language.json"

!macro PadmateOptionsPage
  ; "1" when the desktop shortcut is to be there after the install, else "0";
  ; and the page's box.
  Var padmateDesktop
  Var padmateDesktopBox
  Page custom PadmateOptionsShow PadmateOptionsLeave
!macroend

!macro PadmateOptionFunctions
  ; ( $R8 a Windows language id -- $R8 the installer language for it )
  ; Chinese for every Chinese (the primary language LANG_CHINESE, 0x04), else
  ; English.
  Function PadmateLanguageFor
    Push $0
    IntOp $0 $R8 & 0x3FF
    ${If} $0 = 4
      StrCpy $R8 ${PADMATE_LANG_ZH}
    ${Else}
      StrCpy $R8 ${PADMATE_LANG_EN}
    ${EndIf}
    Pop $0
  FunctionEnd

  ; ( -- $R8 the value of --language= on the command line, "" for none )
  ; The value runs to the next space, without quotes around it. Not GetOptions:
  ; it ends a value at its first "-", the mark the option starts with, so that
  ; zh-CN would be zh.
  Function PadmateLanguageOption
    Push $0
    Push $1
    Push $2
    Push $3
    ${GetParameters} $0
    StrCpy $R8 ""
    StrLen $2 $0
    StrCpy $1 0
    ${Do}
      ${If} $1 >= $2
        ${ExitDo}
      ${EndIf}
      StrCpy $3 $0 11 $1
      ${If} $3 == "--language="
        ; Only at the start of an argument.
        IntOp $3 $1 - 1
        StrCpy $3 $0 1 $3
        ${If} $1 = 0
        ${OrIf} $3 == " "
          IntOp $1 $1 + 11
          StrCpy $R8 $0 "" $1
          ${ExitDo}
        ${EndIf}
      ${EndIf}
      IntOp $1 $1 + 1
    ${Loop}
    ; Up to the next space, and without quotes.
    StrLen $2 $R8
    StrCpy $1 0
    ${Do}
      ${If} $1 >= $2
        ${ExitDo}
      ${EndIf}
      StrCpy $3 $R8 1 $1
      ${If} $3 == " "
        StrCpy $R8 $R8 $1
        ${ExitDo}
      ${EndIf}
      IntOp $1 $1 + 1
    ${Loop}
    ${Do}
      StrCpy $3 $R8 1
      ${If} $3 == '"'
        StrCpy $R8 $R8 "" 1
      ${Else}
        ${ExitDo}
      ${EndIf}
    ${Loop}
    ${Do}
      StrCpy $3 $R8 1 -1
      ${If} $3 == '"'
        StrCpy $R8 $R8 -1
      ${Else}
        ${ExitDo}
      ${EndIf}
    ${Loop}
    Pop $3
    Pop $2
    Pop $1
    Pop $0
  FunctionEnd

  ; ( $R8 what --language= says -- $R8 the installer language it names, ""
  ;   when it names neither )
  Function PadmateLanguageNamed
    ${If} $R8 == "zh-CN"
      StrCpy $R8 ${PADMATE_LANG_ZH}
    ${ElseIf} $R8 == "en"
      StrCpy $R8 ${PADMATE_LANG_EN}
    ${Else}
      StrCpy $R8 ""
    ${EndIf}
  FunctionEnd

  ; ( $R8 an installer language -- $R8 the window's language for it, "zh-CN"
  ;   or "en" )
  Function PadmateUILanguage
    ${If} $R8 = ${PADMATE_LANG_ZH}
      StrCpy $R8 "zh-CN"
    ${Else}
      StrCpy $R8 "en"
    ${EndIf}
  FunctionEnd

  ; Sets $LANGUAGE at the start of a run (.onInit): the language --language=
  ; names, else the one of the system's display language, which a run with
  ; windows offers first in a dialog of both languages. Cancel in the dialog
  ; ends the run with exit code 1, before anything is done.
  Function PadmateChooseLanguage
    Push $0
    Push $R8
    Call PadmateLanguageOption
    Call PadmateLanguageNamed
    ${If} $R8 != ""
      StrCpy $LANGUAGE $R8
    ${Else}
      ; A LANGID is a WORD: the rest of the register is not its.
      System::Call 'kernel32::GetUserDefaultUILanguage()i .R8'
      IntOp $R8 $R8 & 0xFFFF
      Call PadmateLanguageFor
      StrCpy $LANGUAGE $R8
      ${IfNot} ${Silent}
        ; A: every language, also one whose code page the system lacks. The
        ; dialog starts on $LANGUAGE.
        LangDLL::LangDialog "${PADMATE_LANGDLL_TITLE}" "${PADMATE_LANGDLL_TEXT}" A ${MUI_LANGDLL_LANGUAGES} ""
        Pop $0
        ${If} $0 == "cancel"
        ${OrIf} $0 == ""
          SetErrorLevel 1
          Quit
        ${EndIf}
        StrCpy $LANGUAGE $0
      ${EndIf}
    ${EndIf}
    Pop $R8
    Pop $0
  FunctionEnd

  ; ( $R8 a folder, $R9 a language of the window, "zh-CN" or "en" -- "1" on
  ;   the stack when the language file in the folder holds it now, "0" when
  ;   the file was kept or could not be written )
  ; Writes the file when there is none, or when its first line is one the
  ; installer writes; anything else, a language picked on the settings page
  ; above all, is kept. The comparison is exact: a file changed by hand is the
  ; person's too. installerpkg.WritesLanguage is the same rule.
  Function PadmateWriteLanguage
    Push $0
    Push $1
    Push $2
    StrCpy $2 "1"
    ${If} ${FileExists} "$R8\${PADMATE_LANGUAGE_FILE}"
      StrCpy $2 "0"
      ClearErrors
      FileOpen $0 "$R8\${PADMATE_LANGUAGE_FILE}" r
      ${IfNot} ${Errors}
        FileRead $0 $1
        FileClose $0
        ${Do}
          StrCpy $0 $1 1 -1
          ${If} $0 == "$\r"
          ${OrIf} $0 == "$\n"
            StrCpy $1 $1 -1
          ${Else}
            ${ExitDo}
          ${EndIf}
        ${Loop}
        ${If} $1 S== '{"language":"zh-CN","setBy":"installer"}'
        ${OrIf} $1 S== '{"language":"en","setBy":"installer"}'
          StrCpy $2 "1"
        ${EndIf}
      ${EndIf}
    ${EndIf}
    ${If} $2 == "1"
      CreateDirectory "$R8"
      ClearErrors
      FileOpen $0 "$R8\${PADMATE_LANGUAGE_FILE}" w
      ${If} ${Errors}
        StrCpy $2 "0"
      ${Else}
        FileWrite $0 '{"language":"$R9","setBy":"installer"}$\n'
        FileClose $0
      ${EndIf}
    ${EndIf}
    StrCpy $0 $2
    Pop $2
    Pop $1
    Exch $0
  FunctionEnd

  ; ( $R8 the folder an earlier installation recorded, "" for none; $R9 "1"
  ;   when the desktop has the shortcut now -- "1" on the stack when the box
  ;   starts ticked, else "0" )
  ; A first installation makes the shortcut; over an installation the box says
  ; whether there is one, so that a shortcut the person deleted does not come
  ; back with each install. installerpkg.DesktopDefault is the same rule.
  Function PadmateDesktopDefault
    Push $0
    StrCpy $0 "1"
    ${If} $R8 != ""
    ${AndIf} $R9 != "1"
      StrCpy $0 "0"
    ${EndIf}
    Exch $0
  FunctionEnd

  ; Sets $padmateDesktop at the start of a run (.onInit), before an earlier
  ; installation is touched: PadmateDesktopDefault, and "0" for
  ; --no-desktop-shortcut.
  Function PadmateDesktopInit
    Push $R8
    Push $R9
    ReadRegStr $R8 HKCU "${INSTALL_REGISTRY_KEY}" InstallLocation
    StrCpy $R9 "0"
    ${If} ${FileExists} "${PADMATE_DESKTOP_LINK}"
      StrCpy $R9 "1"
    ${EndIf}
    Call PadmateDesktopDefault
    Pop $padmateDesktop
    ${If} ${isNoDesktopShortcut}
      StrCpy $padmateDesktop "0"
    ${EndIf}
    Pop $R9
    Pop $R8
  FunctionEnd

  ; The page skips itself for --updated, as the other pages do. The box keeps
  ; what it says in $padmateDesktop at once, so that Back and Next show it again.
  Function PadmateOptionsShow
    ${If} ${isUpdated}
      Abort
    ${EndIf}
    Push $0
    !insertmacro MUI_HEADER_TEXT "$(padmateOptionsTitle)" "$(padmateOptionsText)"
    nsDialogs::Create 1018
    Pop $0
    ${If} $0 == error
      Pop $0
      Abort
    ${EndIf}
    ${NSD_CreateCheckbox} 0 0 100% 12u "$(padmateDesktopShortcut)"
    Pop $padmateDesktopBox
    ${If} $padmateDesktop == "1"
      ${NSD_Check} $padmateDesktopBox
    ${EndIf}
    ${NSD_OnClick} $padmateDesktopBox PadmateOptionsClick
    Pop $0
    nsDialogs::Show
  FunctionEnd

  Function PadmateOptionsRead
    Push $0
    ${NSD_GetState} $padmateDesktopBox $0
    ${If} $0 == ${BST_CHECKED}
      StrCpy $padmateDesktop "1"
    ${Else}
      StrCpy $padmateDesktop "0"
    ${EndIf}
    Pop $0
  FunctionEnd

  ; ( the box -- ) nsDialogs pushes the box; Exch and Pop drop it and give $0
  ; back.
  Function PadmateOptionsClick
    Exch $0
    Pop $0
    Call PadmateOptionsRead
  FunctionEnd

  Function PadmateOptionsLeave
    Call PadmateOptionsRead
  FunctionEnd
!macroend
