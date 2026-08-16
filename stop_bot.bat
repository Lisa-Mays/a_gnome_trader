@echo off
cd /d "%~dp0"
echo stop > stop.flag
taskkill /IM a_gnome_trader.exe /F >nul 2>&1
echo Bot stopped.
timeout /t 2 /nobreak >nul
