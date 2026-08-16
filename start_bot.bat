@echo off
title a_gnome_trader
cd /d "%~dp0"
if exist stop.flag del stop.flag
:loop
a_gnome_trader.exe
if exist stop.flag (
    del stop.flag
    exit
)
echo.
echo Bot exited. Restarting in 10 seconds... (close this window to stop)
timeout /t 10 /nobreak >nul
goto loop
