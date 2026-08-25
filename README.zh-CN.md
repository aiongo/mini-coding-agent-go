&nbsp;
# mini-coding-agent-go

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.27-00ADD8.svg)](go.mod)

**极简本地 coding agent 的 Go 移植——忠实移植
[Sebastian Raschka 的 mini-coding-agent](https://github.com/rasbt/mini-coding-agent),
不依赖任何 LLM 框架。**

[English](README.md) | **简体中文**

本仓库包含一个小型独立 coding agent:

- 代码:单一 `main` 包(`mini_agent.go`、`tools.go`、`model.go` 等)
- CLI:`mini-coding-agent-go`

它是一个极简的本地 agent 循环,包含:

- 工作区快照采集
- 稳定 prompt + 每轮状态
- 结构化工具
- 危险工具的审批机制
- 会话与记忆持久化
- 有界的子 agent 委托

模型后端目前基于 Ollama。

<a href="https://magazine.sebastianraschka.com/p/components-of-a-coding-agent">
  <img src="https://substack-post-media.s3.amazonaws.com/public/images/49b97718-57f4-4977-99c8-8ad5c4d32af3_1548x862.png" width="500px">
</a>

<br>

**[详细教程:Components of a Coding Agent](https://magazine.sebastianraschka.com/p/components-of-a-coding-agent)**
——本移植逐组件对照 Python 原版实现。

&nbsp;
## 与 Python 原版的差异

Go 版保留了 agent 的内部构造——自定义 `<tool>`/`<final>` 文本协议、ask() 循环、
下面这六大组件——只调整了外围:

- **CLI 形态**:根命令进入交互式 REPL;一次性 prompt 通过 `prompt` 子命令(别名
  `p`)传入,而不是根命令的位置参数。
- **无运行时框架**:resty 直连 Ollama 的 `/api/generate` 原生端点,`urfave/cli`
  做 CLI,`afero` 做文件 IO。不用 LLM 框架,不用 Ollama SDK。
- **`--max-new-tokens` 默认 4096**(Python 默认的 512 会截断整文件的 `write_file` 调用)。
- **调试日志**:模型的请求/响应流量追加写入工作区根目录下的
  `.mini-coding-agent/agent.log`。
- 会话、工作记忆、审批闸口、七个工具、REPL 斜杠命令均与原版一致。

&nbsp;
## 六大核心组件

<a href="https://magazine.sebastianraschka.com/p/components-of-a-coding-agent">
  <img alt="Six core components of a coding agent" src="https://sebastianraschka.com/images/github/mini-coding-agent/six-components.webp" width="500px">
</a>

这个 coding harness 围绕六个实用构建块组织:

1. **实时仓库上下文**
   agent 启动时预先采集稳定的工作区事实:仓库布局、说明文件、git 状态。
2. **Prompt 形态与缓存复用**
   稳定的 prompt 前缀与变化的请求、transcript、记忆分离,重复的模型调用可以高效复用静态部分。
3. **结构化工具、校验与权限**
   模型通过具名工具工作,输入有校验、路径限制在工作区内、危险操作有审批闸口,而非自由发挥的任意动作。
4. **上下文压缩与输出管理**
   长输出被裁剪、重复读取被去重、较旧的 transcript 条目被压缩,以控制 prompt 体积。
5. **Transcript、记忆与恢复**
   运行时同时维护完整的持久 transcript 和更小的工作记忆,会话可恢复且重要状态经由工作记忆保留。
6. **委托与有界子 agent**
   可将限定范围的子任务委托给继承了足够上下文的辅助 agent(但运行在限制之内)。

&nbsp;
## 环境要求

你需要:

- Go 1.27+
- 已安装 Ollama
- 本地已拉取一个 Ollama 模型

&nbsp;
## 安装 Ollama

安装 Ollama,使 `ollama` 命令在 shell 中可用。

官方安装链接:[ollama.com/download](https://ollama.com/download)

验证:

```bash
ollama --help
```

启动服务:

```bash
ollama serve
```

在另一个终端拉取本项目默认使用的模型:

```bash
ollama pull gemma4:cloud
```

agent 只是把 prompt 发给 Ollama 的 `/api/generate` 端点,因此也可以配合你的
Ollama 实例中的其他模型使用。

&nbsp;
## 项目搭建

克隆本仓库(或你的 fork)并进入:

```bash
git clone https://github.com/aiongo/mini-coding-agent-go.git
cd mini-coding-agent-go
```

编译二进制:

```bash
go build        # 产出 ./mini-coding-agent-go
```

或直接安装到你的 `GOBIN`:

```bash
go install github.com/aiongo/mini-coding-agent-go@latest
```

&nbsp;
## 基本用法

启动交互式 REPL:

```bash
./mini-coding-agent-go
```

不进 REPL,跑一次性 prompt:

```bash
./mini-coding-agent-go prompt "检查这个仓库并总结目录结构"
```

(`prompt` 的别名是 `p`。)

默认使用:

- 模型:`gemma4:cloud`
- 审批:`ask`

具体使用示例见 [EXAMPLE.zh-CN.md](EXAMPLE.zh-CN.md)。

&nbsp;
## 审批模式

shell 命令、文件写入等危险工具受审批闸口约束。

- `--approval ask`
  危险操作前询问(默认,推荐)
- `--approval auto`
  自动放行危险操作,包括模型执行任意命令和写文件;仅在你信任 prompt 且信任仓库时使用
- `--approval never`
  拒绝所有危险操作

示例:

```bash
./mini-coding-agent-go --approval auto
```

&nbsp;
## 恢复会话

agent 把会话保存在目标工作区根目录下:

```text
.mini-coding-agent/sessions/
```

恢复最近一次会话:

```bash
./mini-coding-agent-go --resume latest
```

恢复指定会话:

```bash
./mini-coding-agent-go --resume 20260401-144025-2dd0aa
```

&nbsp;
## 交互命令

在 REPL 内,斜杠命令由 agent 直接处理,不会作为普通任务发给模型。

- `/help`
  显示可用交互命令列表
- `/memory`
  打印提炼后的会话记忆,包括当前任务、跟踪的文件和笔记
- `/session`
  打印当前会话 JSON 文件的保存路径
- `/reset`
  清空当前会话历史与提炼记忆,但留在 REPL 中
- `/exit`
  退出交互会话
- `/quit`
  退出交互会话;`/exit` 的别名

&nbsp;
## 主要 CLI 参数

```bash
./mini-coding-agent-go --help
```

CLI 参数在 agent 启动前传入,用于选择工作区、模型连接、恢复行为、审批模式和生成长度限制。

重要参数:

- `--cwd`
  设置 agent 检查和修改的工作区目录;默认:`.`(当前目录)
- `--model`
  选择 Ollama 模型名;默认:`gemma4:cloud`
- `--host`
  指定 Ollama 服务地址(通常无需修改);默认:`http://127.0.0.1:11434`
- `--ollama-timeout`
  客户端等待 Ollama 响应的时长(通常无需修改);默认:`300` 秒
- `--resume`
  按 id 恢复已保存的会话,或用 `latest`;默认:开启新会话
- `--approval`
  控制危险工具的处理方式:`ask`、`auto` 或 `never`;默认:`ask`
- `--max-steps`
  限制一次用户请求允许的模型/工具轮数;默认:`6`
- `--max-new-tokens`
  限制每步模型输出的长度;默认:`4096`
- `--temperature`
  控制采样随机性;默认:`0.2`
- `--top-p`
  控制核采样;默认:`0.9`

&nbsp;
## 示例

见 [EXAMPLE.zh-CN.md](EXAMPLE.zh-CN.md)

&nbsp;
## 说明与提示

- agent 期望模型输出 `<tool>...</tool>` 或 `<final>...</final>` 之一。
- 不同的 Ollama 模型遵循该格式的可靠程度不同。
- 如果模型遵循格式不佳,换用指令遵循能力更强的模型。
- 本 agent 有意保持小巧,为可读性优化,而非健壮性。
- 调试流量(prompt 与原始响应)追加写入工作区根目录下的
  `.mini-coding-agent/agent.log`。

&nbsp;
## 许可证

本仓库代码采用 [MIT License](LICENSE) 许可。

本项目是 [rasbt/mini-coding-agent](https://github.com/rasbt/mini-coding-agent) 的 Go
移植,原项目采用 Apache 2.0 许可;源自原项目的部分保留该许可,见
[LICENSE-APACHE](LICENSE-APACHE)。

&nbsp;
## 致谢

- 原始 Python 实现与设计讲解:
  [Sebastian Raschka — Components of a Coding Agent](https://magazine.sebastianraschka.com/p/components-of-a-coding-agent)
- 原仓库:[rasbt/mini-coding-agent](https://github.com/rasbt/mini-coding-agent)
