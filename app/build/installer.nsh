; What padmate adds to electron-builder's NSIS installer (PKG-03, PKG-04, PKG-05,
; PKG-13). electron-builder includes this file through nsis.include and calls
; the macros below at fixed points: customHeader, customInstallMode and
; customPageAfterChangeDir (while the pages are laid out), customInit,
; customCheckAppRunning (before files are replaced, and before the uninstaller
; starts), customInstall (at the end of the install), customUnInstall.
;
; This file is UTF-8 with a BOM, so that NSIS reads it as Unicode. Keep it so.
;
; The installer is a wizard (ADR-0068): a folder page, the progress and a
; finish page. It installs for the current user only: customInstallMode skips
; electron-builder's page for "all users or only me" for good, and customInit
; ignores /allusers, so nothing ever asks for administrator rights. The
; folder page is padmate's own (customPageAfterChangeDir), not
; electron-builder's allowToChangeInstallationDirectory page: that one has no
; place to hook a check when the person presses Next. The install folder is
; the folder that was chosen when its last component is "padmate", else the
; folder "padmate" inside it, unless the page was left as it offered the
; folder (padmate-wizard.nsh, installerpkg.WizardDir). When that adds the
; name, the page shows the final folder and stays, so that the person sees
; where padmate goes before pressing Next again. NSIS does not judge the text
; while it is typed (that would grey the button for a quoted path or a drive
; root); the page does when Next is pressed: the folder must be named by a
; drive letter or a share, the longest installed path must fit in it, and a
; folder must be made in the closest folder that exists. The old version is
; removed before the new one is written, so a failure halfway would leave
; nothing installed.
;
; The install folder must be empty or padmate's already: the uninstaller
; removes the whole folder, and so must never be pointed at a folder of other
; files. The wizard shows a message and stays on the page. A silent install,
; whose folder /D= names and is used as it is, is refused with exit code 2.
; Padmate's own folder is one that holds padmate.exe or the uninstaller, or the
; one an earlier installation recorded in the registry (a damaged one may have
; lost both).
;
; Before files are replaced or removed (install, upgrade, uninstall) it
; stops the daemon and the window of this installation, and of the earlier
; installation too when the wizard or /D= puts the new one in another folder
; (electron-builder removes the old one after that): padmatectl quit
; --under the installation folder asks the daemon to exit (it lets go of every
; key itself and waits until the process is gone), and what still runs after
; ten seconds is ended. A padmated of another folder, a development build or a
; portable copy, may own the discovery file: --under leaves it alone. When quit
; fails, an old daemon without it or one that does not answer, padmatectl
; release-all lets go of every key first, as ending a daemon would not. It does
; so too before ending a padmated of this folder that is still there after the
; wait although quit exited 0 (it found no discovery file, or a stale one).
; Only padmate.exe, padmated.exe and padmatectl.exe whose file is inside the
; installation folder are ended; a development build or a portable copy
; elsewhere is not.
;
; A full uninstall (not the one an upgrade runs, which gets --updated) also
; removes the login startup entry if it points into this installation, the
; startup argument files that entry needs, %LOCALAPPDATA%\padmate\run and the
; logs; it asks whether to keep the profiles, scripts and settings in
; %APPDATA%\padmate, the window's own settings (its browser data, in ui)
; among them. A silent uninstall (/S) keeps them.
;
; electron-builder's installer leaves a copy of itself, as large as it is, in
; %LOCALAPPDATA%\padmate-updater, for the differential download of
; electron-updater, and its uninstaller does not remove it. padmate's update
; (PKG-10) downloads whole installers (disableDifferentialDownload), so
; customInstall deletes the copy as soon as it is made, and a full uninstall
; deletes it again in case that failed. Versions before v0.17.0-rc.3 leave it,
; also after their uninstall.
;
; The same folder holds what the update downloaded, in pending\: an update
; that was never installed. A full uninstall deletes the folder with all of it
; (RMDir /r). The install does not: during an update the installer that runs is
; the file in pending\, and an upgrade's uninstaller (--updated) skips the
; clean-up. The application deletes the pending installer of an update it has
; installed when it starts. An update runs the installer as --updated /S
; --force-run: silent, and the template then starts padmate again as the user
; through the shell. /S alone, as the installer's smoke test and a person who
; installs silently run it, starts nothing.
;
; TEST ONLY: when the environment variable PADMATE_INSTALLER_TEST_ROOT names an
; existing folder, the installer and uninstaller use <root>\AppData\Roaming and
; <root>\AppData\Local instead of %APPDATA% and %LOCALAPPDATA%, the registry
; value HKCU\Software\padmate-installer-test\Run\padmate instead of the real
; Run value, pass --ipc-dir <root>\AppData\Local\padmate\run to padmatectl quit,
; and do not run padmatectl release-all (it signals every running padmated of
; the user). daemon/internal/installersmoke sets it; nothing else should.

!ifdef BUILD_UNINSTALLER
  !define PADMATE_UN "un."
!else
  !define PADMATE_UN ""
!endif

; The folder rule of the wizard: only a macro, expanded in customHeader.
!include "padmate-wizard.nsh"

; The folder of that copy (APP_INSTALLER_STORE_FILE, relative to %LOCALAPPDATA%;
; electron-builder names it after the package). A build where it is named
; otherwise fails here rather than leave the copy.
!define PADMATE_UPDATER_DIR "padmate-updater"
!if "${APP_INSTALLER_STORE_FILE}" != "${PADMATE_UPDATER_DIR}\installer.exe"
  !error "APP_INSTALLER_STORE_FILE is ${APP_INSTALLER_STORE_FILE}, not ${PADMATE_UPDATER_DIR}\installer.exe"
!endif

; PROCESSENTRY32W in a 32-bit installer: nine 4-byte fields and MAX_PATH wide
; characters. The pointer-sized th32DefaultHeapID is the only field that would
; differ in a 64-bit one.
!define PADMATE_ENTRY_SIZE 556

!macro customHeader
  ; Test root, or "" for a real run, and where the paths of this run are.
  Var padmateTestRoot
  Var padmateRoaming
  Var padmateLocal
  Var padmateRunKey
  Var padmateIpc
  ; The installation folder being stopped, whether to end what is left, how
  ; many programs of it were found, and how many of them are padmated.exe.
  Var padmateDir
  Var padmateKill
  Var padmateCount
  Var padmateDaemons

  !ifndef BUILD_UNINSTALLER
    LangString padmateFolderNotEmpty ${LANG_ENGLISH} "The folder $INSTDIR already holds other files, and uninstalling padmate would remove them.$\r$\nClear the folder, or run the installer again with /D= naming an empty folder."
    LangString padmateFolderNotEmpty ${LANG_SIMPCHINESE} "文件夹 $INSTDIR 里已有其他文件，卸载 padmate 时会一并删除。$\r$\n请先清空该文件夹，或用 /D= 指定一个空文件夹后重新运行安装程序。"
  !endif

  !ifdef BUILD_UNINSTALLER
    Var padmateData
    LangString padmateKeepData ${LANG_ENGLISH} "Keep your profiles and scripts?$\r$\n$\r$\nYes keeps the profiles, scripts and settings in $padmateData for a later install.$\r$\nNo deletes everything in that folder."
    LangString padmateKeepData ${LANG_SIMPCHINESE} "是否保留配置和脚本？$\r$\n$\r$\n“是”保留 $padmateData 中的配置、脚本和设置，重新安装后可继续使用。$\r$\n“否”删除该文件夹中的全部内容。"
  !endif

  !ifndef BUILD_UNINSTALLER
    LangString padmateDirForeign ${LANG_ENGLISH} "The folder $INSTDIR already holds other files, and uninstalling padmate would remove them. Choose another location."
    LangString padmateDirForeign ${LANG_SIMPCHINESE} "文件夹 $INSTDIR 里已有其他文件，卸载 padmate 时会一并删除。请选择别的位置。"
    LangString padmateDirInvalid ${LANG_ENGLISH} "Enter the full path of a folder, such as D:\Apps."
    LangString padmateDirInvalid ${LANG_SIMPCHINESE} "请输入完整的文件夹路径，例如 D:\Apps。"
    LangString padmateDirTooLong ${LANG_ENGLISH} "The path of the folder $INSTDIR is too long. Choose a shorter location."
    LangString padmateDirTooLong ${LANG_SIMPCHINESE} "文件夹 $INSTDIR 的路径太长。请选择较短的位置。"
    LangString padmateDirNoWrite ${LANG_ENGLISH} "Cannot write to the folder $INSTDIR. Choose another location."
    LangString padmateDirNoWrite ${LANG_SIMPCHINESE} "无法写入文件夹 $INSTDIR。请选择别的位置。"

    !insertmacro PadmatePathFunctions
    !insertmacro PadmatePageFunctions

    ; The check of a silent install, and of one that has no folder page
    ; (--updated), at the start: the folder is the one /D= names, or the
    ; recorded one. The folder page of the wizard checks on Next instead.
    Function PadmateCheckFolder
      Push $0
      Call PadmateFolderIsForeign
      Pop $0
      ${If} $0 == "1"
        MessageBox MB_OK|MB_ICONSTOP "$(padmateFolderNotEmpty)" /SD IDOK
        SetErrorLevel 2
        Quit
      ${EndIf}
      Pop $0
    FunctionEnd
  !endif

  ; Sets padmateTestRoot, padmateRoaming, padmateLocal, padmateRunKey and
  ; padmateIpc for this run.
  Function ${PADMATE_UN}PadmateLocate
    ReadEnvStr $padmateTestRoot PADMATE_INSTALLER_TEST_ROOT
    StrCpy $padmateRoaming "$APPDATA"
    StrCpy $padmateLocal "$LOCALAPPDATA"
    StrCpy $padmateRunKey "Software\Microsoft\Windows\CurrentVersion\Run"
    StrCpy $padmateIpc ""
    ${If} $padmateTestRoot != ""
    ${AndIf} ${FileExists} "$padmateTestRoot\*.*"
      StrCpy $padmateRoaming "$padmateTestRoot\AppData\Roaming"
      StrCpy $padmateLocal "$padmateTestRoot\AppData\Local"
      StrCpy $padmateRunKey "Software\padmate-installer-test\Run"
      StrCpy $padmateIpc ' --ipc-dir "$padmateLocal\padmate\run"'
    ${Else}
      StrCpy $padmateTestRoot ""
    ${EndIf}
  FunctionEnd

  ; Counts the programs named padmate.exe, padmated.exe or padmatectl.exe
  ; whose file is inside $padmateDir, in $padmateCount (of them, the
  ; padmated.exe in $padmateDaemons), and ends them when $padmateKill is 1. A
  ; program of the same name elsewhere is not touched.
  ; Windows reports the process list through the Toolhelp snapshot and each
  ; program's file through QueryFullProcessImageName; both sides of the
  ; comparison are put in long form, as a path may be given in either.
  Function ${PADMATE_UN}PadmateScan
    Push $0
    Push $1
    Push $2
    Push $3
    Push $4
    Push $5
    Push $6
    Push $7
    Push $8
    Push $9
    Push $R0
    Push $R1
    Push $R2
    Push $R3
    StrCpy $padmateCount 0
    StrCpy $padmateDaemons 0
    StrCpy $0 $padmateDir
    System::Call 'kernel32::GetLongPathNameW(w r0, w .r1, i ${NSIS_MAX_STRLEN})i .r2'
    ${If} $2 <> 0
      StrCpy $0 $1
    ${EndIf}
    StrCpy $1 $0 1 -1
    ${If} $1 != "\"
      StrCpy $0 "$0\"
    ${EndIf}
    StrLen $2 $0
    ; TH32CS_SNAPPROCESS
    System::Call 'kernel32::CreateToolhelp32Snapshot(i 2, i 0)p .r3'
    System::Call '*(i ${PADMATE_ENTRY_SIZE},i,i,p,i,i,i,i,i,&w260)p .r4'
    System::Call 'kernel32::Process32FirstW(p r3, p r4)i .r5'
    ${While} $5 <> 0
      System::Call '*$4(i,i,i .r6,p,i,i,i,i,i,&w260 .r7)'
      ${If} $7 == "padmate.exe"
      ${OrIf} $7 == "padmated.exe"
      ${OrIf} $7 == "padmatectl.exe"
        ; PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE
        System::Call 'kernel32::OpenProcess(i 0x101000, i 0, i r6)p .r8'
        ${If} $8 <> 0
          System::Call 'kernel32::QueryFullProcessImageNameW(p r8, i 0, w .r9, *i ${NSIS_MAX_STRLEN})i .R0'
          ${If} $R0 <> 0
            System::Call 'kernel32::GetLongPathNameW(w r9, w .R1, i ${NSIS_MAX_STRLEN})i .R2'
            ${If} $R2 <> 0
              StrCpy $9 $R1
            ${EndIf}
            StrCpy $R3 $9 $2
            ${If} $R3 == $0
              IntOp $padmateCount $padmateCount + 1
              ${If} $7 == "padmated.exe"
                IntOp $padmateDaemons $padmateDaemons + 1
              ${EndIf}
              ${If} $padmateKill = 1
                ; PROCESS_TERMINATE | SYNCHRONIZE
                System::Call 'kernel32::OpenProcess(i 0x100001, i 0, i r6)p .R1'
                ${If} $R1 <> 0
                  System::Call 'kernel32::TerminateProcess(p R1, i 1)'
                  System::Call 'kernel32::WaitForSingleObject(p R1, i 5000)'
                  System::Call 'kernel32::CloseHandle(p R1)'
                ${EndIf}
              ${EndIf}
            ${EndIf}
          ${EndIf}
          System::Call 'kernel32::CloseHandle(p r8)'
        ${EndIf}
      ${EndIf}
      System::Call 'kernel32::Process32NextW(p r3, p r4)i .r5'
    ${EndWhile}
    System::Call 'kernel32::CloseHandle(p r3)'
    System::Free $4
    Pop $R3
    Pop $R2
    Pop $R1
    Pop $R0
    Pop $9
    Pop $8
    Pop $7
    Pop $6
    Pop $5
    Pop $4
    Pop $3
    Pop $2
    Pop $1
    Pop $0
  FunctionEnd

  ; Stops the programs of the installation in $padmateDir (PKG-04): asks the
  ; daemon to exit and lets go of every key, waits up to ten seconds for the
  ; programs to exit, and ends what is left.
  Function ${PADMATE_UN}PadmateStopDir
    Push $0
    Push $1
    Push $2
    ; Whether release-all has run.
    StrCpy $2 0
    ${If} ${FileExists} "$padmateDir\resources\bin\padmatectl.exe"
      DetailPrint "padmatectl quit"
      ; --under: the discovery file may name a padmated of another folder (a
      ; development build, a portable copy). Only one of this folder is asked.
      nsExec::Exec /TIMEOUT=30000 '"$padmateDir\resources\bin\padmatectl.exe" quit --timeout 10s --under "$padmateDir"$padmateIpc'
      Pop $0
      ; quit exits 0 when the daemon has exited, none runs, or the one that
      ; runs is of another folder. Otherwise keys may be held, and release-all
      ; is the one thing that lets go of them without IPC. It signals every
      ; padmated of the user that sends keys, not only the one of this folder,
      ; so it is not used when quit worked, nor in a test.
      ${If} $0 != "0"
      ${AndIf} $padmateTestRoot == ""
        nsExec::Exec /TIMEOUT=20000 '"$padmateDir\resources\bin\padmatectl.exe" release-all'
        Pop $0
        StrCpy $2 1
      ${EndIf}
    ${EndIf}
    StrCpy $1 0
    ${Do}
      StrCpy $padmateKill 0
      Call ${PADMATE_UN}PadmateScan
      ${If} $padmateCount = 0
      ${OrIf} $1 >= 40
        ${Break}
      ${EndIf}
      IntOp $1 $1 + 1
      Sleep 250
    ${Loop}
    ${If} $padmateCount > 0
      DetailPrint "padmate: ending what is left"
      ; A padmated of this folder is still there although quit exited 0: it
      ; found no discovery file, an unreadable or stale one, or a pid of
      ; another program. Ending it would leave its keys held, so they are let
      ; go of first, if that has not been done. Not for a stuck window or
      ; padmatectl, which hold no keys, nor in a test.
      ${If} $padmateDaemons > 0
      ${AndIf} $2 = 0
      ${AndIf} $padmateTestRoot == ""
      ${AndIf} ${FileExists} "$padmateDir\resources\bin\padmatectl.exe"
        DetailPrint "padmatectl release-all"
        nsExec::Exec /TIMEOUT=20000 '"$padmateDir\resources\bin\padmatectl.exe" release-all'
        Pop $0
        ; The daemon lets go of the keys on its own thread.
        Sleep 500
      ${EndIf}
      StrCpy $padmateKill 1
      Call ${PADMATE_UN}PadmateScan
    ${EndIf}
    Pop $2
    Pop $1
    Pop $0
  FunctionEnd

  ; Stops this installation, and the one the registry names if it is
  ; another folder (the wizard or /D= put the new installation elsewhere).
  Function ${PADMATE_UN}PadmateStop
    Push $0
    Call ${PADMATE_UN}PadmateLocate
    StrCpy $padmateDir $INSTDIR
    Call ${PADMATE_UN}PadmateStopDir
    !ifndef BUILD_UNINSTALLER
      ReadRegStr $0 HKCU "${INSTALL_REGISTRY_KEY}" InstallLocation
      ${If} $0 != ""
      ${AndIf} $0 != $INSTDIR
        StrCpy $padmateDir $0
        Call PadmateStopDir
      ${EndIf}
    !endif
    Pop $0
  FunctionEnd

  ; Deletes the copy electron-builder's installer keeps of itself (see the top
  ; of this file), and its folder when nothing else is in it. The template
  ; writes it to the real %LOCALAPPDATA%, test root or not, so it is removed
  ; from there.
  Function ${PADMATE_UN}PadmateDropInstallerCopy
    Delete "$LOCALAPPDATA\${APP_INSTALLER_STORE_FILE}"
    RMDir "$LOCALAPPDATA\${PADMATE_UPDATER_DIR}"
  FunctionEnd

  !ifdef BUILD_UNINSTALLER
    ; The clean-up of a full uninstall (PKG-05).
    Function un.PadmateClean
      Push $0
      Push $1
      Push $2
      Push $3
      Call un.PadmateLocate
      StrCpy $padmateData "$padmateRoaming\padmate"

      ; The login startup entry, if it starts this installation's daemon.
      ReadRegStr $0 HKCU "$padmateRunKey" "padmate"
      ${If} $0 != ""
        StrCpy $1 $0 1
        ${If} $1 == '"'
          StrCpy $0 $0 "" 1
        ${EndIf}
        StrCpy $1 "$INSTDIR\"
        StrLen $2 $1
        StrCpy $3 $0 $2
        ${If} $3 == $1
          DeleteRegValue HKCU "$padmateRunKey" "padmate"
          StrCpy $0 ""
        ${EndIf}
      ${EndIf}
      ; The test key is the installer's own: removed once nothing is in it.
      ; (DeleteRegKey /ifempty looks at subkeys only, not at values.)
      ${If} $padmateTestRoot != ""
        EnumRegValue $1 HKCU "$padmateRunKey" 0
        ${If} $1 == ""
          DeleteRegKey /ifempty HKCU "$padmateRunKey"
          DeleteRegKey /ifempty HKCU "Software\padmate-installer-test"
        ${EndIf}
      ${EndIf}
      ; The argument files of a long startup command are the entry's: keep them
      ; while an entry of another padmate still needs them.
      ${If} $0 == ""
        RMDir /r "$padmateData\startup"
      ${EndIf}

      ; The discovery file and token of the daemon, and the held-key log.
      RMDir /r "$padmateLocal\padmate\run"
      RMDir "$padmateLocal\padmate"

      ; Again, in case the delete at the end of the install failed (a program
      ; had the file open).
      Call un.PadmateDropInstallerCopy

      ; And what the update downloaded (PKG-10): the folder of the copy, with
      ; pending\ in it. Only a full uninstall deletes it, never an install or
      ; an upgrade, whose installer may be the file in pending\. The real
      ; folder, as the template's copy is; a test root has its own.
      ${If} $padmateTestRoot == ""
        RMDir /r "$LOCALAPPDATA\${PADMATE_UPDATER_DIR}"
      ${Else}
        RMDir /r "$padmateLocal\${PADMATE_UPDATER_DIR}"
      ${EndIf}

      ; The logs hold nothing of the user's own. The window's browser data
      ; (ui) does: its language, label display and icon cache. It is kept with
      ; the profiles and scripts, and goes with them when they are deleted.
      RMDir /r "$padmateData\logs"

      ; Profiles, scripts and settings: kept by a silent uninstall (/S); asked
      ; otherwise. The MessageBox has no /SD on purpose: it is asked unless /S is
      ; on the command line, whatever mode the uninstaller runs in.
      ${StdUtils.TestParameter} $0 "S"
      ${If} $0 != "true"
        MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON1 "$(padmateKeepData)" IDYES padmateKeep
        ${If} $padmateRoaming != ""
          RMDir /r "$padmateData"
        ${EndIf}
      ${EndIf}
      padmateKeep:
      ; Empty only when nothing of the user's is left.
      RMDir "$padmateData"
      Pop $3
      Pop $2
      Pop $1
      Pop $0
    FunctionEnd
  !endif
!macroend

; electron-builder's page for "all users or only me" is part of the wizard.
; This is expanded inside its function and ends it with Abort, which skips the
; page: the installation is for the current user, and the code after this
; macro, which would ask for administrator rights on /allusers, never runs.
; (The uninstaller keeps electron-builder's logic, which skips the page when
; the installation is the current user's.)
!macro customInstallMode
  !ifndef BUILD_UNINSTALLER
    StrCpy $hasPerMachineInstallation "0"
    StrCpy $hasPerUserInstallation "1"
    !insertmacro setInstallModePerUser
    Abort
  !endif
!macroend

; The folder page. It stands in for the one electron-builder adds with
; allowToChangeInstallationDirectory (see the top of this file), and is laid
; out where that one would be: after the install-mode page, before the
; progress. Its functions are in customHeader.
!macro customPageAfterChangeDir
  !insertmacro PadmateDirPage
!macroend

!macro customInit
  ; Current user only. /allusers, or a record of a machine-wide installation,
  ; would have made it "all users", which asks for administrator rights.
  StrCpy $hasPerMachineInstallation "0"
  StrCpy $hasPerUserInstallation "1"
  !insertmacro setInstallModePerUser
  ; A silent install, and one with no folder page (--updated), check the
  ; folder here. The wizard checks it when Next is pressed.
  ${If} ${Silent}
  ${OrIf} ${isUpdated}
    Call PadmateCheckFolder
  ${EndIf}
!macroend

; The check before files are replaced or removed. electron-builder's own
; version kills by program name, wherever the program is.
!macro customCheckAppRunning
  Call ${PADMATE_UN}PadmateStop
!macroend

; After the files are written and the registry entries made.
!macro customInstall
  Call PadmateDropInstallerCopy
!macroend

!macro customUnInstall
  ${ifNot} ${isUpdated}
    Call un.PadmateClean
  ${endIf}
!macroend
