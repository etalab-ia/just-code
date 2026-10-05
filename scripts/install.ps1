#Requires -Version 5.1
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$script:JustCodeRepository = 'etalab-ia/just-code'
$script:JustCodeApi = "https://api.github.com/repos/$script:JustCodeRepository"
$script:JustCodeDownload = "https://github.com/$script:JustCodeRepository/releases/download"

function Get-JustCodeLatestRelease {
	$headers = @{
		Accept = 'application/vnd.github+json'
		'User-Agent' = 'just-code-installer'
	}
	Invoke-RestMethod -Uri "$script:JustCodeApi/releases/latest" -Headers $headers -ErrorAction Stop
}

function Get-JustCodeAssetName {
	param([Parameter(Mandatory = $true)][string]$Architecture)

	switch ($Architecture.ToUpperInvariant()) {
		{ $_ -in @('AMD64', 'X64') } { return 'just-code-windows-x64.exe' }
		'ARM64' { return 'just-code-windows-arm64.exe' }
		default { throw "Unsupported Windows architecture '$Architecture'; supported targets are x64 and arm64." }
	}
}

function Test-JustCodeWindows {
	[System.Environment]::OSVersion.Platform -eq [System.PlatformID]::Win32NT
}

function Save-JustCodeReleaseAsset {
	param(
		[Parameter(Mandatory = $true)][string]$Uri,
		[Parameter(Mandatory = $true)][string]$Path
	)

	$headers = @{ 'User-Agent' = 'just-code-installer' }
	Invoke-WebRequest -Uri $Uri -OutFile $Path -Headers $headers -MaximumRedirection 10 -UseBasicParsing -ErrorAction Stop
	if (-not (Test-Path -LiteralPath $Path -PathType Leaf) -or (Get-Item -LiteralPath $Path).Length -eq 0) {
		throw "Download was empty: $Uri"
	}
}

function Invoke-JustCodeSetup {
	param([Parameter(Mandatory = $true)][string]$Path)

	& $Path setup | Out-Host
	$LASTEXITCODE
}

function Replace-JustCodeBinary {
	param(
		[Parameter(Mandatory = $true)][string]$StagedPath,
		[Parameter(Mandatory = $true)][string]$TargetPath,
		[Parameter(Mandatory = $true)][string]$BackupPath
	)

	if (Test-Path -LiteralPath $TargetPath) {
		if (Test-Path -LiteralPath $BackupPath) {
			Remove-Item -LiteralPath $BackupPath -Force
		}
		[System.IO.File]::Replace($StagedPath, $TargetPath, $BackupPath)
	} else {
		[System.IO.File]::Move($StagedPath, $TargetPath)
	}
}

function Install-JustCode {
	[CmdletBinding()]
	param(
		[string]$InstallDirectory,
		[string]$Architecture
	)

	if (-not (Test-JustCodeWindows)) {
		throw 'This installer supports Windows only; use install.sh on macOS or Linux.'
	}

	if (-not $Architecture) {
		$Architecture = $env:PROCESSOR_ARCHITEW6432
		if (-not $Architecture) { $Architecture = $env:PROCESSOR_ARCHITECTURE }
	}
	$asset = Get-JustCodeAssetName -Architecture $Architecture

	if (-not $InstallDirectory) {
		if (-not $env:LOCALAPPDATA) {
			throw 'LOCALAPPDATA is not set; cannot choose a user-owned installation directory.'
		}
		$InstallDirectory = Join-Path $env:LOCALAPPDATA 'Programs/just-code'
	}

	$InstallDirectory = [System.IO.Path]::GetFullPath($InstallDirectory)
	if (Test-Path -LiteralPath $InstallDirectory) {
		$installItem = Get-Item -LiteralPath $InstallDirectory -Force
		if (-not $installItem.PSIsContainer) {
			throw "$InstallDirectory is not a directory."
		}
	} else {
		$null = New-Item -ItemType Directory -Path $InstallDirectory -Force
	}
	$target = Join-Path $InstallDirectory 'just-code.exe'
	$backup = Join-Path $InstallDirectory 'just-code.previous.exe'
	if (Test-Path -LiteralPath $target) {
		$current = Get-Item -LiteralPath $target -Force
		if ($current.PSIsContainer -or ($current.Attributes -band [System.IO.FileAttributes]::ReparsePoint)) {
			throw "$target is not a regular file; refusing to replace it."
		}
	}

	$tempDirectory = Join-Path $InstallDirectory ".just-code-install-$([guid]::NewGuid().ToString('N'))"
	$null = New-Item -ItemType Directory -Path $tempDirectory
	try {
		$release = Get-JustCodeLatestRelease
		$tag = [string]$release.tag_name
		if ($tag -notmatch '^just-code-v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$') {
			throw "Unexpected GitHub release tag '$tag'."
		}

		$binaryPath = Join-Path $tempDirectory $asset
		$checksumPath = Join-Path $tempDirectory 'SHA256SUMS'
		$binaryUri = "$script:JustCodeDownload/$tag/$asset"
		$checksumUri = "$script:JustCodeDownload/$tag/SHA256SUMS"
		Save-JustCodeReleaseAsset -Uri $binaryUri -Path $binaryPath
		Save-JustCodeReleaseAsset -Uri $checksumUri -Path $checksumPath

		$checksumText = [System.IO.File]::ReadAllText($checksumPath, [System.Text.Encoding]::ASCII)
		$rows = @([regex]::Matches($checksumText, '(?m)^(?<hash>[0-9a-fA-F]{64})\s+\*?(?<name>\S+)\s*$') | Where-Object {
			$_.Groups['name'].Value -ceq $asset
		})
		if ($rows.Count -ne 1) {
			throw "SHA256SUMS from release $tag does not contain exactly one entry for $asset."
		}
		$expected = $rows[0].Groups['hash'].Value.ToLowerInvariant()
		$actual = (Get-FileHash -LiteralPath $binaryPath -Algorithm SHA256).Hash.ToLowerInvariant()
		if ($actual -cne $expected) {
			throw "Checksum mismatch for $asset; the existing installation was not changed."
		}

		Replace-JustCodeBinary -StagedPath $binaryPath -TargetPath $target -BackupPath $backup

		$setupExitCode = Invoke-JustCodeSetup -Path $target
		if ($setupExitCode -ne 0) {
			$recovery = if (Test-Path -LiteralPath $backup) {
				' Previous binary retained at just-code.previous.exe for rollback.'
			} else {
				''
			}
			throw "just-code was installed, but setup exited with status $setupExitCode.$recovery"
		}

		Write-Host "Installed $asset from release $tag at $target"
		$pathEntries = @($env:Path -split ';' | ForEach-Object { $_.TrimEnd('\') })
		if (-not ($pathEntries | Where-Object { $_ -ieq $InstallDirectory.TrimEnd('\') })) {
			Write-Host 'Add the installation directory to PATH for future shells. For this PowerShell session run:'
			Write-Host '  $env:Path = "$env:LOCALAPPDATA\Programs\just-code;$env:Path"'
			Write-Host 'The installer does not edit user profiles or environment settings.'
		}
	} finally {
		if (Test-Path -LiteralPath $tempDirectory) {
			Remove-Item -LiteralPath $tempDirectory -Recurse -Force
		}
	}
}

if ($MyInvocation.InvocationName -ne '.') {
	try {
		Install-JustCode
	} catch {
		[Console]::Error.WriteLine("just-code installer: {0}", $_.Exception.Message)
		exit 1
	}
}
