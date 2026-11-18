package version

// Version 由构建参数注入：-X mtun/internal/version.Version=x.y.z
var Version = "dev"

// Repo 是 GitHub 仓库（owner/name），用于在线更新；
// 构建时可用 -X mtun/internal/version.Repo=owner/name 注入，
// 运行时可用环境变量 MTUN_UPDATE_REPO 覆盖。
var Repo = ""
