param(
    [string]$ReleaseDirectory = "",
    [string]$PreviousReleaseDirectory = ""
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$release = if ([string]::IsNullOrWhiteSpace($ReleaseDirectory)) {
    Join-Path $root "Nodren-1.0.1-windows-x64"
} else {
    (Resolve-Path -LiteralPath $ReleaseDirectory).Path
}
$previousRelease = if ([string]::IsNullOrWhiteSpace($PreviousReleaseDirectory)) {
    Join-Path $root "Nodren-1.0.0-windows-x64"
} else {
    (Resolve-Path -LiteralPath $PreviousReleaseDirectory).Path
}
$expected = @("NodrenApp.exe", "nodren.exe", "nodren-worker.exe", "nodren-core.dll")
$expectedRelease = $expected + @("Install-Nodren.exe", "Install-Nodren-Worker.exe")
$compiler = Join-Path $root ".artifacts\tools\InnoSetup-6.7.3\ISCC.exe"
foreach ($source in @($release, $previousRelease)) {
    if (-not (Test-Path -LiteralPath $source)) {
        throw "Release input is missing: $source"
    }
    $names = @(Get-ChildItem -LiteralPath $source -File | Select-Object -ExpandProperty Name | Sort-Object)
    $requiredNames = if ($source -eq $release) { $expectedRelease } else { $expected }
    if (@(Compare-Object ($requiredNames | Sort-Object) $names -CaseSensitive).Count -ne 0) {
        throw "Release input '$source' must contain exactly: $($requiredNames -join ', ')"
    }
}
if (-not (Test-Path -LiteralPath $compiler)) {
    throw "Pinned Inno Setup compiler not found. Run scripts\build-installers.ps1 first."
}

function Invoke-Setup([string]$SetupPath, [string]$Directory, [string]$LogPath, [switch]$AddPath) {
    $arguments = @("/CURRENTUSER", "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-",
        "/DIR=`"$Directory`"", "/LOG=`"$LogPath`"")
    if ($AddPath) {
        $arguments += "/TASKS=addtopath"
    } else {
        $arguments += "/TASKS="
    }
    $process = Start-Process -FilePath $SetupPath -ArgumentList $arguments -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "Installer failed with exit code $($process.ExitCode): $SetupPath (see $LogPath)"
    }
}

function Invoke-Uninstall([string]$Directory, [string]$LogPath) {
    $uninstaller = Get-ChildItem -LiteralPath $Directory -Filter "unins*.exe" -File -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending | Select-Object -First 1 -ExpandProperty FullName
    if ([string]::IsNullOrWhiteSpace($uninstaller)) {
        throw "Uninstaller was not registered in $Directory"
    }
    $process = Start-Process -FilePath $uninstaller -ArgumentList @(
        "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/LOG=`"$LogPath`""
    ) -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "Uninstaller failed with exit code $($process.ExitCode): $uninstaller"
    }
}

function Assert-InstalledFiles([string]$Directory, [string[]]$Names, [string]$SourceDirectory = "") {
    foreach ($name in $Names) {
        $path = Join-Path $Directory $name
        if (-not (Test-Path -LiteralPath $path -PathType Leaf) -or (Get-Item -LiteralPath $path).Length -le 0) {
            throw "Expected installed file is missing or empty: $path"
        }
        $source = Join-Path $SourceDirectory $name
        if (-not [string]::IsNullOrWhiteSpace($SourceDirectory) -and (Test-Path -LiteralPath $source) -and
            (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash -ne
            (Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash) {
            throw "Installed file integrity check failed: $path"
        }
    }
}

function Assert-Uninstalled([string]$Directory) {
    if (@(Get-ChildItem -LiteralPath $Directory -Filter "unins*.exe" -File -ErrorAction SilentlyContinue).Count -ne 0) {
        throw "Uninstaller still exists after removal: $Directory"
    }
    foreach ($name in @("NodrenApp.exe", "nodren.exe", "nodren-worker.exe", "nodren-core.dll", "Worker-README.txt")) {
        if (Test-Path -LiteralPath (Join-Path $Directory $name)) {
            throw "Installed file remains after uninstall: $(Join-Path $Directory $name)"
        }
    }
}

function Wait-ForController([string]$Url) {
    $deadline = (Get-Date).AddSeconds(15)
    while ((Get-Date) -lt $deadline) {
        try {
            $health = Invoke-RestMethod -Uri "$Url/health" -TimeoutSec 1
            if ($health.status -eq "ok" -and $health.service -eq "nodren-controller") {
                return
            }
        } catch {
            Start-Sleep -Milliseconds 250
        }
    }
    throw "Installed NodrenApp did not start its managed Controller."
}

$testRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("nodren-installer-" + [guid]::NewGuid().ToString("N"))
$previousOutput = Join-Path $testRoot "previous-installers"
$currentOutput = Join-Path $testRoot "current-installers"
$desktopDirectory = Join-Path $testRoot "Desktop"
$workerDirectory = Join-Path $testRoot "Worker"
$stateFile = Join-Path $testRoot "controller-state.json"
$roamingNodren = Join-Path $env:APPDATA "Nodren"
$roamingNodrenExisted = Test-Path -LiteralPath $roamingNodren
$preservedFile = Join-Path $roamingNodren ("installer-upgrade-test-" + [guid]::NewGuid().ToString("N") + ".txt")
$registry = [Microsoft.Win32.Registry]::CurrentUser
$environmentKey = $registry.OpenSubKey("Environment", $true)
if ($null -eq $environmentKey) {
    $environmentKey = $registry.CreateSubKey("Environment")
}
$originalPathExists = $null -ne $environmentKey.GetValue("Path", $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
$originalPath = $environmentKey.GetValue("Path", "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
$originalPathKind = if ($originalPathExists) { $environmentKey.GetValueKind("Path") } else { $null }
$workerEnvironmentNames = @("NODREN_CONTROLLER_ADDR", "NODREN_WORKER_ID")
$originalWorkerEnvironment = @{}
foreach ($name in $workerEnvironmentNames) {
    $originalWorkerEnvironment[$name] = @{
        Exists = $null -ne $environmentKey.GetValue($name, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        Value = $environmentKey.GetValue($name, "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        Kind = if ($null -ne $environmentKey.GetValue($name, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)) {
            $environmentKey.GetValueKind($name)
        } else { $null }
    }
}
$oldEnvironment = @{}
foreach ($name in @("NODREN_STATE_FILE", "NODREN_AUTH_MODE", "NODREN_CONTROLLER_URL", "NODREN_HTTP_ADDR", "NODREN_NODE_ADDR", "NODREN_CORE_LIBRARY", "NODREN_MANAGED_CONTROLLER_URL", "NODREN_API_TOKEN", "NODREN_WORKER_TOKEN", "NODREN_WORKER_ID", "NODREN_WORKER_TOKENS", "NODREN_WORKER_CONCURRENCY", "NODREN_HTTP_CERT_FILE", "NODREN_HTTP_KEY_FILE", "NODREN_SHUTDOWN_TOKEN")) {
    $oldEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}
$oldProcessPath = $env:PATH
$oldInstallerVariables = @{}
foreach ($name in @("NODREN_INSTALLER_RELEASE_DIR", "NODREN_INSTALLER_OUTPUT_DIR", "NODREN_INSTALLER_VERSION")) {
    $oldInstallerVariables[$name] = [Environment]::GetEnvironmentVariable($name, "Process")
}
$appProcess = $null
$workerProcess = $null
$testSucceeded = $false

try {
    New-Item -ItemType Directory -Path $testRoot -Force | Out-Null
    New-Item -ItemType Directory -Path $roamingNodren -Force | Out-Null
    if (Test-Path -LiteralPath $preservedFile) {
        throw "Refusing to overwrite existing test sentinel: $preservedFile"
    }
    Set-Content -LiteralPath $preservedFile -Value "preserve-across-upgrade-and-uninstall" -NoNewline

    foreach ($port in @(8080, 9000)) {
        $probe = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $port)
        try {
            $probe.Start()
        } catch {
            throw "Installer integration test requires unused local Controller port $port."
        } finally {
            $probe.Stop()
        }
    }

    Write-Host "[1/7] Compiling previous-version installers for upgrade coverage"
    New-Item -ItemType Directory -Path $previousOutput -Force | Out-Null
    $env:NODREN_INSTALLER_RELEASE_DIR = $previousRelease
    $env:NODREN_INSTALLER_OUTPUT_DIR = $previousOutput
    $env:NODREN_INSTALLER_VERSION = "1.0.0"
    $desktopTestId = [guid]::NewGuid().ToString().ToUpperInvariant()
    $workerTestId = [guid]::NewGuid().ToString().ToUpperInvariant()
    $desktopSource = Get-Content -LiteralPath (Join-Path $PSScriptRoot "installers\Nodren.iss") -Raw
    $desktopSource = $desktopSource.Replace("AppId={{6C2F776B-2C5E-4C7B-969D-22B5459FA101}", "AppId={{$desktopTestId}")
    $workerSource = Get-Content -LiteralPath (Join-Path $PSScriptRoot "installers\NodrenWorker.iss") -Raw
    $workerSource = $workerSource.Replace("AppId={{6C2F776B-2C5E-4C7B-969D-22B5459FA102}", "AppId={{$workerTestId}")
    $workerReadme = Join-Path $PSScriptRoot "installers\Worker-README.txt"
    $workerSource = $workerSource.Replace('Source: "Worker-README.txt"', "Source: `"$workerReadme`"")
    $encoding = [System.Text.UTF8Encoding]::new($false)
    $previousDesktopIss = Join-Path $previousOutput "Nodren-Test.iss"
    $previousWorkerIss = Join-Path $previousOutput "NodrenWorker-Test.iss"
    [System.IO.File]::WriteAllText($previousDesktopIss, $desktopSource, $encoding)
    [System.IO.File]::WriteAllText($previousWorkerIss, $workerSource, $encoding)
    foreach ($script in @($previousDesktopIss, $previousWorkerIss)) {
        & $compiler $script
        if ($LASTEXITCODE -ne 0) { throw "Could not compile previous-version installer: $script" }
    }
    New-Item -ItemType Directory -Path $currentOutput -Force | Out-Null
    $env:NODREN_INSTALLER_RELEASE_DIR = $release
    $env:NODREN_INSTALLER_OUTPUT_DIR = $currentOutput
    $env:NODREN_INSTALLER_VERSION = "1.0.1"
    $currentDesktopIss = Join-Path $currentOutput "Nodren-Test.iss"
    $currentWorkerIss = Join-Path $currentOutput "NodrenWorker-Test.iss"
    [System.IO.File]::WriteAllText($currentDesktopIss, $desktopSource, $encoding)
    [System.IO.File]::WriteAllText($currentWorkerIss, $workerSource, $encoding)
    foreach ($script in @($currentDesktopIss, $currentWorkerIss)) {
        & $compiler $script
        if ($LASTEXITCODE -ne 0) { throw "Could not compile current-version test installer: $script" }
    }

    Write-Host "[2/7] Installing previous desktop version and upgrading to 1.0.1"
    Invoke-Setup (Join-Path $previousOutput "Install-Nodren.exe") $desktopDirectory `
        (Join-Path $testRoot "desktop-1.0.0-install.log") -AddPath
    Assert-InstalledFiles $desktopDirectory @("NodrenApp.exe", "nodren.exe", "nodren-worker.exe", "nodren-core.dll") $previousRelease
    $expectedPath = if ($originalPathExists -and $originalPath) { "$originalPath;$desktopDirectory" } else { $desktopDirectory }
    $currentUserPath = $environmentKey.GetValue("Path", "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    if ($currentUserPath -ne $expectedPath) {
        throw "Desktop installer did not preserve and append the CLI PATH correctly."
    }
    Invoke-Setup (Join-Path $currentOutput "Install-Nodren.exe") $desktopDirectory `
        (Join-Path $testRoot "desktop-1.0.1-upgrade.log") -AddPath
    Assert-InstalledFiles $desktopDirectory @("NodrenApp.exe", "nodren.exe", "nodren-worker.exe", "nodren-core.dll") $release
    if ((Get-Content -LiteralPath $preservedFile -Raw) -ne "preserve-across-upgrade-and-uninstall") {
        throw "Desktop user data was not preserved during upgrade."
    }

    Write-Host "[3/7] Launching installed desktop app and exercising CLI/Worker/Core"
    $env:NODREN_STATE_FILE = $stateFile
    $env:NODREN_AUTH_MODE = "development"
    foreach ($name in @("NODREN_CONTROLLER_URL", "NODREN_HTTP_ADDR", "NODREN_NODE_ADDR", "NODREN_CORE_LIBRARY", "NODREN_MANAGED_CONTROLLER_URL", "NODREN_API_TOKEN", "NODREN_WORKER_TOKEN", "NODREN_WORKER_TOKENS", "NODREN_HTTP_CERT_FILE", "NODREN_HTTP_KEY_FILE", "NODREN_SHUTDOWN_TOKEN")) {
        Remove-Item "Env:$name" -ErrorAction SilentlyContinue
    }
    $appProcess = Start-Process -FilePath (Join-Path $desktopDirectory "NodrenApp.exe") `
        -WorkingDirectory $desktopDirectory -PassThru
    Wait-ForController "http://127.0.0.1:8080"
    $deadline = (Get-Date).AddSeconds(15)
    while ((Get-Date) -lt $deadline) {
        $appProcess.Refresh()
        if ($appProcess.HasExited) { throw "Installed NodrenApp exited with code $($appProcess.ExitCode)." }
        if ($appProcess.MainWindowHandle -ne [IntPtr]::Zero) { break }
        Start-Sleep -Milliseconds 250
    }
    if ($appProcess.MainWindowHandle -eq [IntPtr]::Zero) {
        throw "Installed NodrenApp did not create its desktop window."
    }

    $cli = Join-Path $desktopDirectory "nodren.exe"
    $help = (& $cli --help 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $help -notmatch "Usage:") { throw "Installed CLI --help failed: $help" }
    $env:PATH = "$desktopDirectory;$oldProcessPath"
    Push-Location $testRoot
    try {
        $pathHelp = (& nodren --help 2>&1 | Out-String)
        if ($LASTEXITCODE -ne 0 -or $pathHelp -notmatch "Usage:") {
            throw "CLI could not be invoked by name from the installed PATH entry: $pathHelp"
        }
    } finally {
        Pop-Location
    }
    $status = (& $cli status 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $status -notmatch "Controller\s+ONLINE") { throw "Installed CLI status failed: $status" }

    $env:NODREN_WORKER_ID = "INSTALLER-DESKTOP-WORKER"
    $workerProcess = Start-Process -FilePath (Join-Path $desktopDirectory "nodren-worker.exe") `
        -WorkingDirectory $desktopDirectory -PassThru -WindowStyle Hidden `
        -ArgumentList @("--controller", "127.0.0.1:9000", "--id", "INSTALLER-DESKTOP-WORKER") `
        -RedirectStandardOutput (Join-Path $testRoot "desktop-worker.log") `
        -RedirectStandardError (Join-Path $testRoot "desktop-worker.err")
    $deadline = (Get-Date).AddSeconds(15)
    $registered = $false
    while ((Get-Date) -lt $deadline) {
        try {
            $nodes = @(Invoke-RestMethod "http://127.0.0.1:8080/v1/nodes" -TimeoutSec 1)
            if ($nodes.Count -eq 1 -and $nodes[0].state -eq "READY") { $registered = $true; break }
        } catch { }
        Start-Sleep -Milliseconds 250
    }
    if (-not $registered) { throw "Installed Worker did not register with the Controller." }
    $run = (& $cli run sum 1 2 3 4 5 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $run -notmatch "Result\s+15") { throw "Installed workload execution failed: $run" }
    Stop-Process -Id $workerProcess.Id -Force
    $workerProcess.WaitForExit(5000) | Out-Null
    $workerProcess = $null

    Write-Host "[4/7] Installing previous Worker version and upgrading to 1.0.1"
    foreach ($name in $workerEnvironmentNames) {
        $environmentKey.SetValue($name, "preserve-$name", [Microsoft.Win32.RegistryValueKind]::String)
    }
    Invoke-Setup (Join-Path $previousOutput "Install-Nodren-Worker.exe") $workerDirectory `
        (Join-Path $testRoot "worker-1.0.0-install.log")
    Assert-InstalledFiles $workerDirectory @("nodren-worker.exe", "nodren-core.dll") $previousRelease
    Assert-InstalledFiles $workerDirectory @("Worker-README.txt") (Join-Path $PSScriptRoot "installers")
    Invoke-Setup (Join-Path $currentOutput "Install-Nodren-Worker.exe") $workerDirectory `
        (Join-Path $testRoot "worker-1.0.1-upgrade.log")
    Assert-InstalledFiles $workerDirectory @("nodren-worker.exe", "nodren-core.dll") $release
    Assert-InstalledFiles $workerDirectory @("Worker-README.txt") (Join-Path $PSScriptRoot "installers")
    foreach ($name in $workerEnvironmentNames) {
        if ($environmentKey.GetValue($name, "") -ne "preserve-$name") {
            throw "Worker upgrade modified existing configuration variable $name."
        }
    }
    $workerHelp = (& (Join-Path $workerDirectory "nodren-worker.exe") --help 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $workerHelp -notmatch "controller") { throw "Installed Worker --help failed." }
    $env:NODREN_AUTH_MODE = "development"
    $workerProcess = Start-Process -FilePath (Join-Path $workerDirectory "nodren-worker.exe") `
        -WorkingDirectory $workerDirectory -PassThru -WindowStyle Hidden `
        -ArgumentList @("--controller", "127.0.0.1:9000", "--id", "INSTALLER-WORKER-ONLY") `
        -RedirectStandardOutput (Join-Path $testRoot "worker-only.log") `
        -RedirectStandardError (Join-Path $testRoot "worker-only.err")
    $deadline = (Get-Date).AddSeconds(15)
    $workerRegistered = $false
    while ((Get-Date) -lt $deadline) {
        try {
            $nodes = @(Invoke-RestMethod "http://127.0.0.1:8080/v1/nodes" -TimeoutSec 1)
            if ($nodes.Count -eq 1 -and $nodes[0].info.id -eq "INSTALLER-WORKER-ONLY" -and
                $nodes[0].state -eq "READY") { $workerRegistered = $true; break }
        } catch { }
        Start-Sleep -Milliseconds 250
    }
    if (-not $workerRegistered) { throw "Worker-only installation did not register with the Controller." }
    $workerRun = (& $cli run sum 2 3 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $workerRun -notmatch "Result\s+5") {
        throw "Workload execution through the Worker-only installation failed: $workerRun"
    }
    Stop-Process -Id $workerProcess.Id -Force
    $workerProcess.WaitForExit(5000) | Out-Null
    $workerProcess = $null
    $shutdown = Start-Process -FilePath (Join-Path $desktopDirectory "NodrenApp.exe") `
        -WorkingDirectory $desktopDirectory -ArgumentList @("--shutdown") -PassThru
    if (-not $shutdown.WaitForExit(5000) -or $shutdown.ExitCode -ne 0 -or -not $appProcess.WaitForExit(15000)) {
        throw "Installed NodrenApp did not shut down its managed Controller cleanly."
    }
    $appProcess = $null

    Write-Host "[5/7] Uninstalling, checking data preservation, then reinstalling"
    Invoke-Uninstall $desktopDirectory (Join-Path $testRoot "desktop-uninstall.log")
    Assert-Uninstalled $desktopDirectory
    if ($originalPathExists) {
        if ($environmentKey.GetValue("Path", "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) -ne $originalPath) {
            throw "Desktop uninstaller did not restore the pre-existing user PATH."
        }
    } elseif ($null -ne $environmentKey.GetValue("Path", $null)) {
        throw "Desktop uninstaller left its user PATH value behind."
    }
    if ((Get-Content -LiteralPath $preservedFile -Raw) -ne "preserve-across-upgrade-and-uninstall") {
        throw "Desktop user data was not preserved during uninstall."
    }
    Invoke-Setup (Join-Path $currentOutput "Install-Nodren.exe") $desktopDirectory `
        (Join-Path $testRoot "desktop-reinstall.log") -AddPath
    Assert-InstalledFiles $desktopDirectory @("NodrenApp.exe", "nodren.exe", "nodren-worker.exe", "nodren-core.dll") $release
    Invoke-Uninstall $desktopDirectory (Join-Path $testRoot "desktop-reinstall-uninstall.log")
    Assert-Uninstalled $desktopDirectory

    Invoke-Uninstall $workerDirectory (Join-Path $testRoot "worker-uninstall.log")
    Assert-Uninstalled $workerDirectory
    foreach ($name in $workerEnvironmentNames) {
        if ($environmentKey.GetValue($name, "") -ne "preserve-$name") {
            throw "Worker uninstaller modified existing configuration variable $name."
        }
    }
    Invoke-Setup (Join-Path $currentOutput "Install-Nodren-Worker.exe") $workerDirectory `
        (Join-Path $testRoot "worker-reinstall.log")
    Assert-InstalledFiles $workerDirectory @("nodren-worker.exe", "nodren-core.dll") $release
    Assert-InstalledFiles $workerDirectory @("Worker-README.txt") (Join-Path $PSScriptRoot "installers")
    Invoke-Uninstall $workerDirectory (Join-Path $testRoot "worker-reinstall-uninstall.log")
    Assert-Uninstalled $workerDirectory

    Write-Host "[6/7] Verifying final user settings and PATH state"
    $testSucceeded = $true
    Write-Host "[7/7] PASS: desktop/worker install, v1.0.0-to-v1.0.1 upgrade, execution, PATH, uninstall, and reinstall"
}
finally {
    if ($null -ne $workerProcess -and -not $workerProcess.HasExited) {
        Stop-Process -Id $workerProcess.Id -Force
        $workerProcess.WaitForExit(5000) | Out-Null
    }
    if ($null -ne $appProcess -and -not $appProcess.HasExited) {
        try {
            $shutdownPath = Join-Path $desktopDirectory "NodrenApp.exe"
            if (Test-Path -LiteralPath $shutdownPath) {
                $shutdown = Start-Process -FilePath $shutdownPath -ArgumentList @("--shutdown") -Wait -PassThru
                if ($shutdown.ExitCode -ne 0) { throw "Shutdown request exited $($shutdown.ExitCode)" }
            }
        } catch {
            Stop-Process -Id $appProcess.Id -Force -ErrorAction SilentlyContinue
        }
    }
    foreach ($name in $oldEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($name, $oldEnvironment[$name], "Process")
    }
    $env:PATH = $oldProcessPath
    foreach ($name in $oldInstallerVariables.Keys) {
        [Environment]::SetEnvironmentVariable($name, $oldInstallerVariables[$name], "Process")
    }
    foreach ($directory in @($desktopDirectory, $workerDirectory)) {
        for ($attempt = 0; $attempt -lt 3; $attempt++) {
            $uninstaller = Get-ChildItem -LiteralPath $directory -Filter "unins*.exe" -File -ErrorAction SilentlyContinue |
                Sort-Object Name -Descending | Select-Object -First 1 -ExpandProperty FullName
            if ([string]::IsNullOrWhiteSpace($uninstaller)) { break }
            try {
                Start-Process -FilePath $uninstaller -ArgumentList @(
                    "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"
                ) -Wait | Out-Null
            } catch {
                Write-Warning "Could not automatically remove test installation at $directory"
                break
            }
        }
    }
    foreach ($name in $workerEnvironmentNames) {
        $saved = $originalWorkerEnvironment[$name]
        if ($saved.Exists) { $environmentKey.SetValue($name, $saved.Value, $saved.Kind) }
        else { $environmentKey.DeleteValue($name, $false) }
    }
    if ($originalPathExists) { $environmentKey.SetValue("Path", $originalPath, $originalPathKind) }
    else { $environmentKey.DeleteValue("Path", $false) }
    $environmentKey.Dispose()
    if ($testSucceeded) {
        Remove-Item -LiteralPath $testRoot -Recurse -Force -ErrorAction SilentlyContinue
    }
    Remove-Item -LiteralPath $preservedFile -Force -ErrorAction SilentlyContinue
    if (-not $roamingNodrenExisted -and (Test-Path -LiteralPath $roamingNodren) -and
        @(Get-ChildItem -LiteralPath $roamingNodren -Force).Count -eq 0) {
        Remove-Item -LiteralPath $roamingNodren -Force
    }
}
