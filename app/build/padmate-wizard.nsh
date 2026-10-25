; The folder page of padmate's installation wizard (PKG-13, ADR-0068), apart
; from installer.nsh so that tests can compile and run it by themselves
; (daemon/internal/installerpkg/wizard_nsis_windows_test.go runs the rule,
; daemon/internal/installersmoke/wizardpage_windows_test.go the page). The Go
; function installerpkg.WizardDir does what the rule does, and tests keep the
; two equal.
;
; This file is ASCII only and defines three macros: PadmatePathFunctions, the
; rule that turns what was typed into the install folder and the checks of a
; folder that need no disk; PadmatePageFunctions, the functions of the page that
; uses them (below), which also look at the disk; and PadmateDirPage, the page
; itself. The first two expand their functions once. The caller includes
; LogicLib and defines APP_FILENAME (the name of the folder, "padmate") before
; expanding the first.
;
; The functions take and give their data in registers or on the stack, as noted
; on each, and leave every other register as they found it.

; The longest path inside an installation (the backslash in front of it
; included), and the longest path Windows takes without the \\?\ prefix.
; installerpkg.MaxRelativePath and MaxPath are the same numbers; a test keeps
; them equal, and CheckTree holds the installed files to the first.
!define PADMATE_MAX_REL 140
!define PADMATE_MAX_PATH 259

!macro PadmatePathFunctions
  ; ( $R8 a path as typed -- $R8 the path without the marks around it )
  ; Cuts spaces and quotes off both ends, and backslashes off the end, so that
  ; "D:\Apps\" and ' D:\Apps ' name the same folder. A pasted path often has
  ; quotes around it. A drive "D:\" becomes "D:".
  Function PadmateTrimPath
    Push $0
    ${Do}
      StrCpy $0 $R8 1 -1
      ${If} $0 == "\"
      ${OrIf} $0 == " "
      ${OrIf} $0 == '"'
        StrCpy $R8 $R8 -1
      ${Else}
        ${ExitDo}
      ${EndIf}
    ${Loop}
    ${Do}
      StrCpy $0 $R8 1
      ${If} $0 == " "
      ${OrIf} $0 == '"'
        StrCpy $R8 $R8 "" 1
      ${Else}
        ${ExitDo}
      ${EndIf}
    ${Loop}
    Pop $0
  FunctionEnd

  ; ( $R9 the folder the page offered, $R8 the text of its field --
  ;   $R8 the install folder, $R9 "1" when the name was added, else "0" )
  ; The install folder is what was typed when its last component is the
  ; folder name (any case), else the folder named inside it. A field left as
  ; the page offered it is used as it is, so that an upgrade keeps the folder
  ; of the old installation. An empty field gives an empty folder.
  Function PadmateWizardDir
    Push $0
    Push $1
    Push $2
    Push $3
    Push $4
    Push $5
    StrCpy $1 $R9
    Call PadmateTrimPath
    StrCpy $2 $R8
    StrCpy $R8 $1
    Call PadmateTrimPath
    StrCpy $1 $R8
    StrCpy $R8 $2
    StrCpy $R9 "0"
    ${If} $2 != ""
    ${AndIf} $2 != $1
      ; The last component of what was typed, in $3.
      StrLen $4 $2
      StrCpy $3 $2
      StrCpy $0 0
      ${Do}
        IntOp $0 $0 + 1
        ${If} $0 > $4
          ${ExitDo}
        ${EndIf}
        StrCpy $5 $2 1 -$0
        ${If} $5 == "\"
          IntOp $0 $0 - 1
          StrCpy $3 $2 "" -$0
          ${ExitDo}
        ${EndIf}
      ${Loop}
      ${If} $3 != "${APP_FILENAME}"
        StrCpy $R8 "$2\${APP_FILENAME}"
        StrCpy $R9 "1"
      ${EndIf}
    ${EndIf}
    Pop $5
    Pop $4
    Pop $3
    Pop $2
    Pop $1
    Pop $0
  FunctionEnd

  ; ( $R8 a folder -- "1" on the stack when it is named by a drive letter or a
  ;   network share and has no character Windows refuses, else "0" )
  ; The page does not let NSIS judge the text (it would grey the Install button
  ; for a quoted path and for a drive root, before the text is tidied), so this
  ; says what a folder the installer can use looks like: D:, D:\x, \\server.
  Function PadmateIsFullPath
    Push $0
    Push $1
    Push $2
    Push $3
    Push $4
    ; $3: the answer. $1: where the part after the drive or "\\" starts.
    StrCpy $3 "0"
    StrLen $2 $R8
    StrCpy $0 $R8 2
    ${If} $0 == "\\"
      ${If} $2 > 2
        StrCpy $3 "1"
        StrCpy $1 2
      ${EndIf}
    ${Else}
      StrCpy $0 $R8 1
      StrCpy $1 $R8 1 1
      ${If} $1 == ":"
        ; The drive must be a letter (NSIS compares strings without regard to
        ; case).
        StrCpy $2 0
        ${Do}
          StrCpy $4 "ABCDEFGHIJKLMNOPQRSTUVWXYZ" 1 $2
          ${If} $4 == ""
            ${ExitDo}
          ${EndIf}
          ${If} $4 == $0
            StrCpy $3 "1"
            ${ExitDo}
          ${EndIf}
          IntOp $2 $2 + 1
        ${Loop}
        ; Then nothing, or a backslash.
        StrCpy $4 $R8 1 2
        ${If} $4 != ""
        ${AndIf} $4 != "\"
          StrCpy $3 "0"
        ${EndIf}
        StrCpy $1 2
      ${EndIf}
    ${EndIf}
    ${If} $3 == "1"
      ${Do}
        StrCpy $0 $R8 1 $1
        ${If} $0 == ""
          ${ExitDo}
        ${EndIf}
        ${If} $0 == "/"
        ${OrIf} $0 == ":"
        ${OrIf} $0 == "*"
        ${OrIf} $0 == "?"
        ${OrIf} $0 == '"'
        ${OrIf} $0 == "<"
        ${OrIf} $0 == ">"
        ${OrIf} $0 == "|"
          StrCpy $3 "0"
          ${ExitDo}
        ${EndIf}
        IntOp $1 $1 + 1
      ${Loop}
    ${EndIf}
    StrCpy $0 $3
    Pop $4
    Pop $3
    Pop $2
    Pop $1
    Exch $0
  FunctionEnd

  ; ( $R8 a folder -- "1" on the stack when the longest file of an installation
  ;   fits in it, else "0" )
  ; Windows takes 259 characters without the \\?\ prefix, and the installer
  ; writes its files without it. installerpkg.FitsPath is the same check.
  Function PadmateFitsPath
    Push $0
    StrLen $0 $R8
    IntOp $0 $0 + ${PADMATE_MAX_REL}
    ${If} $0 > ${PADMATE_MAX_PATH}
      StrCpy $0 "0"
    ${Else}
      StrCpy $0 "1"
    ${EndIf}
    Exch $0
  FunctionEnd
!macroend

; The functions of the folder page: Pre, Show, Leave, and the checks of the
; folder. The installer expands this in customHeader (build/installer.nsh); the
; test of daemon/internal/installersmoke/wizardpage_windows_test.go expands it
; in a wizard of its own, so that the page is run as it is. The caller has
;   - defined the LangStrings of the messages: padmateDirInvalid (what was typed
;     is no folder), padmateDirTooLong, padmateDirForeign (a folder of other
;     files) and padmateDirNoWrite; the symbols INSTALL_REGISTRY_KEY,
;     APP_EXECUTABLE_FILENAME and UNINSTALL_FILENAME; and the condition
;     ${isUpdated},
;   - included LogicLib and nsDialogs, and expanded PadmatePathFunctions,
;   - laid out the page with PadmateDirPage, which declares the Var
;     padmateOffered (the folder the page offered) that Pre and Leave use.
; Expand it once, and only in the installer.
!macro PadmatePageFunctions
  ; ( -- "1" when $INSTDIR holds files that are not padmate's own, else "0" )
  ; The uninstaller removes the whole installation folder, as electron-builder's
  ; does, so the folder must be empty or already padmate's.
  Function PadmateFolderIsForeign
    Push $0
    Push $1
    Push $2
    StrCpy $2 "0"
    ${If} ${FileExists} "$INSTDIR\${APP_EXECUTABLE_FILENAME}"
    ${OrIf} ${FileExists} "$INSTDIR\${UNINSTALL_FILENAME}"
      Goto padmateFolderOwn
    ${EndIf}
    ; The folder an earlier installation recorded is padmate's even when a
    ; damaged one has lost both files. Without this, a reinstall over it
    ; would be refused for good. (INSTDIR is the recorded folder unless the
    ; wizard or /D= names another.)
    ReadRegStr $0 HKCU "${INSTALL_REGISTRY_KEY}" InstallLocation
    ${If} $0 != ""
    ${AndIf} $0 == $INSTDIR
      Goto padmateFolderOwn
    ${EndIf}
    FindFirst $0 $1 "$INSTDIR\*.*"
    ${DoWhile} $1 != ""
      ${If} $1 != "."
      ${AndIf} $1 != ".."
        StrCpy $2 "1"
        ${ExitDo}
      ${EndIf}
      FindNext $0 $1
    ${Loop}
    FindClose $0
    padmateFolderOwn:
    StrCpy $0 $2
    Pop $2
    Pop $1
    Exch $0
  FunctionEnd

  ; It skips itself for --updated, as electron-builder's pages do, and
  ; remembers the folder it offers.
  Function PadmateDirPre
    ${If} ${isUpdated}
      Abort
    ${EndIf}
    StrCpy $padmateOffered $INSTDIR
  FunctionEnd

  ; The page before this one is the install-mode page, which is skipped every
  ; time (customInstallMode), so there is no way back.
  Function PadmateDirShow
    GetDlgItem $0 $HWNDPARENT 3
    EnableWindow $0 0
  FunctionEnd

  ; ( $R8 a folder -- "1" on the stack when the installer can make files in it,
  ;   else "0" )
  ; Tries it, because the rights of a folder are known only to the system: the
  ; folder itself if it is there (the files go into it), else the closest
  ; folder above it that is (the folder is made in it). The test leaves
  ; nothing behind, and makes nothing of the folder to install into, so that
  ; cancelling the wizard leaves no empty folder. It makes a folder, not a file,
  ; above: a user may make folders in the root of a drive but not files.
  Function PadmateCanWrite
    Push $0
    Push $1
    Push $2
    Push $3
    Push $4
    StrCpy $0 $R8
    StrCpy $3 "0"
    ${Do}
      ${If} ${FileExists} "$0\*.*"
        ClearErrors
        ${If} $0 == $R8
          FileOpen $1 "$0\.padmate-write-test" w
          ${IfNot} ${Errors}
            FileClose $1
            Delete "$0\.padmate-write-test"
            StrCpy $3 "1"
          ${EndIf}
        ${Else}
          CreateDirectory "$0\.padmate-write-test"
          ${IfNot} ${Errors}
            RMDir "$0\.padmate-write-test"
            StrCpy $3 "1"
          ${EndIf}
        ${EndIf}
        ${ExitDo}
      ${EndIf}
      ; Up one level: cut at the last backslash. Nothing left, or a bare
      ; backslash, means there is no folder to try (a drive that is not there).
      StrCpy $1 0
      ${Do}
        IntOp $1 $1 + 1
        StrCpy $2 $0 1 -$1
        ${If} $2 == ""
        ${OrIf} $2 == "\"
          ${ExitDo}
        ${EndIf}
      ${Loop}
      ${If} $2 == ""
        ${ExitDo}
      ${EndIf}
      StrCpy $0 $0 -$1
      ${If} $0 == ""
      ${OrIf} $0 == "\"
        ${ExitDo}
      ${EndIf}
    ${Loop}
    StrCpy $0 $3
    Pop $4
    Pop $3
    Pop $2
    Pop $1
    Exch $0
  FunctionEnd

  ; Next. $INSTDIR may not hold the text of the field yet, so the field is
  ; read. The folder the install will use is put in the field and in $INSTDIR.
  ; A text that is no folder, a folder that is too long, one of other files and
  ; one that cannot be written get a message; a name that was added is shown
  ; first. Abort stays on the page.
  Function PadmateDirLeave
    FindWindow $0 "#32770" "" $HWNDPARENT
    GetDlgItem $0 $0 1019
    ${NSD_GetText} $0 $R8
    StrCpy $R9 $padmateOffered
    Call PadmateWizardDir
    ; The checks that need no disk come first, the one that makes something last.
    Call PadmateIsFullPath
    Pop $1
    ${If} $1 != "1"
      MessageBox MB_OK|MB_ICONSTOP "$(padmateDirInvalid)"
      Abort
    ${EndIf}
    ${NSD_GetText} $0 $1
    ${If} $1 != $R8
      ${NSD_SetText} $0 "$R8"
    ${EndIf}
    StrCpy $INSTDIR $R8
    Call PadmateFitsPath
    Pop $1
    ${If} $1 != "1"
      MessageBox MB_OK|MB_ICONSTOP "$(padmateDirTooLong)"
      Abort
    ${EndIf}
    Call PadmateFolderIsForeign
    Pop $1
    ${If} $1 == "1"
      MessageBox MB_OK|MB_ICONSTOP "$(padmateDirForeign)"
      Abort
    ${EndIf}
    Call PadmateCanWrite
    Pop $1
    ${If} $1 != "1"
      MessageBox MB_OK|MB_ICONSTOP "$(padmateDirNoWrite)"
      Abort
    ${EndIf}
    ${If} $R9 == "1"
      Abort
    ${EndIf}
  FunctionEnd
!macroend

; The folder page itself: the standard NSIS directory page with the functions
; above. Expand it once, where the pages are laid out. NSIS checks the text only
; on leaving (MUI_DIRECTORYPAGE_VERIFYONLEAVE): its own check, while the person
; types, would grey the Install button for a quoted path or a drive root
; before PadmateDirLeave could tidy the text, and say nothing about it.
!macro PadmateDirPage
  Var padmateOffered
  !define MUI_PAGE_CUSTOMFUNCTION_PRE PadmateDirPre
  !define MUI_PAGE_CUSTOMFUNCTION_SHOW PadmateDirShow
  !define MUI_PAGE_CUSTOMFUNCTION_LEAVE PadmateDirLeave
  !define MUI_DIRECTORYPAGE_VERIFYONLEAVE
  !insertmacro MUI_PAGE_DIRECTORY
!macroend
