' Launches the bot with no visible window. Logs still go to bot.log.
' Use stop_bot.bat to shut it down.
Dim fso, sh, dir
Set fso = CreateObject("Scripting.FileSystemObject")
dir = fso.GetParentFolderName(WScript.ScriptFullName)
Set sh = CreateObject("WScript.Shell")
sh.CurrentDirectory = dir
sh.Run """" & dir & "\start_bot.bat""", 0, False
