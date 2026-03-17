use std::{fs, path::PathBuf};
use zed_extension_api::{self as zed, Command, LanguageServerId, Result, Worktree};

struct DevtimeExtension {
    cached_binary_path: Option<PathBuf>,
}

impl DevtimeExtension {
    fn target_triple(&self) -> std::result::Result<String, String> {
        let (platform, arch) = zed::current_platform();
        let arch = match arch {
            zed::Architecture::Aarch64 => "aarch64",
            zed::Architecture::X8664 => "x86_64",
            _ => return Err(format!("unsupported architecture: {arch:?}")),
        };
        let os = match platform {
            zed::Os::Mac => "apple-darwin",
            zed::Os::Linux => "unknown-linux-gnu",
            zed::Os::Windows => "pc-windows-msvc",
        };
        Ok(format!("devtime-ls-{arch}-{os}"))
    }

    fn binary_path(&mut self, id: &LanguageServerId) -> Result<PathBuf> {
        if let Some(path) = &self.cached_binary_path {
            if fs::metadata(path).is_ok_and(|m| m.is_file()) {
                return Ok(path.clone());
            }
        }

        zed::set_language_server_installation_status(
            id,
            &zed::LanguageServerInstallationStatus::CheckingForUpdate,
        );

        let release = zed::latest_github_release(
            "arnaudhrt/zed-devtime",
            zed::GithubReleaseOptions {
                require_assets: true,
                pre_release: false,
            },
        )?;

        let triple = self.target_triple()?;
        let asset_name = format!("{triple}.zip");
        let asset = release
            .assets
            .iter()
            .find(|a| a.name == asset_name)
            .ok_or_else(|| format!("no asset found: {asset_name}"))?;

        let version_dir = format!("devtime-ls-{}", release.version);
        let binary_name = match zed::current_platform() {
            (zed::Os::Windows, _) => "devtime-ls.exe",
            _ => "devtime-ls",
        };
        let binary_path = PathBuf::from(&version_dir).join(binary_name);

        if !fs::metadata(&binary_path).is_ok_and(|m| m.is_file()) {
            zed::set_language_server_installation_status(
                id,
                &zed::LanguageServerInstallationStatus::Downloading,
            );
            zed::download_file(
                &asset.download_url,
                &version_dir,
                zed::DownloadedFileType::Zip,
            )
            .map_err(|e| format!("download failed: {e}"))?;

            // Clean old versions
            if let Ok(entries) = fs::read_dir(".") {
                for entry in entries.flatten() {
                    if let Some(name) = entry.file_name().to_str() {
                        if name.starts_with("devtime-ls-") && name != version_dir {
                            fs::remove_dir_all(entry.path()).ok();
                        }
                    }
                }
            }
        }

        zed::make_file_executable(binary_path.to_str().unwrap())?;
        self.cached_binary_path = Some(binary_path.clone());
        Ok(binary_path)
    }
}

impl zed::Extension for DevtimeExtension {
    fn new() -> Self {
        Self {
            cached_binary_path: None,
        }
    }

    fn language_server_command(
        &mut self,
        id: &LanguageServerId,
        worktree: &Worktree,
    ) -> Result<Command> {
        let binary = self.binary_path(id)?;
        let project_folder = worktree.root_path();

        let mut args = vec![];
        if !project_folder.is_empty() {
            args.push("--project-folder".to_string());
            args.push(project_folder.to_string());
        }

        Ok(Command {
            command: binary.to_str().unwrap().to_owned(),
            args,
            env: worktree.shell_env(),
        })
    }
}

zed::register_extension!(DevtimeExtension);
