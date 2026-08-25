&nbsp;
# 交互式示例

[English](EXAMPLE.md) | **简体中文**

这是用 `mini-coding-agent-go` 配合 Ollama 在小型 Python 项目上的实操示例流程。

包含两个示例:

- **示例 1**:实现、修改并测试二分查找函数。
- **示例 2**:构建一个对比两种排序算法的基准测试应用,跑在真实数据文件上。

本示例假设:

- `ollama serve` 已在运行
- 默认模型 `gemma4:cloud` 已在你的 Ollama 实例中可用(例如通过
  `ollama pull gemma4:cloud`)
- 你已克隆 `aiongo/mini-coding-agent-go`
- 你已在本地的 `mini-coding-agent-go` 目录里执行过 `go build`

&nbsp;
## 示例 1:实现二分查找

流程为:

1. 创建一个全新仓库
2. 启动 agent
3. 实现 `binary_search.py`
4. 修改实现
5. 添加 `pytest` 测试
6. 运行测试
7. 修复失败项

&nbsp;
### 1. 创建全新仓库

```bash
cd mini-coding-agent-go
mkdir -p ./tmp/binary-search-repo
cd ./tmp/binary-search-repo
git init
```

此时仓库基本是空的:

```bash
ls -la
```

&nbsp;
### 2. 启动 agent

从你的 `mini-coding-agent-go` 克隆目录启动 agent,并指向新仓库:

```bash
cd mini-coding-agent-go
./mini-coding-agent-go --cwd ./tmp/binary-search-repo
```

使用默认模型 `gemma4:cloud` 运行。

(下方截图来自 Python 原版;Go 版 REPL 使用相同的 `mini-coding-agent>` 提示符和相同的流程。)

如果不想进 REPL,可以用 `prompt` 子命令(别名 `p`)跑单个 prompt:

```bash
./mini-coding-agent-go --cwd ./tmp/binary-search-repo \
  prompt "检查这个仓库并总结目录结构"
```

<img src="https://sebastianraschka.com/images/github/mini-coding-agent/1.webp" width="500px">



&nbsp;

### 3. 让它实现二分查找

在 `mini-coding-agent>` 提示符下粘贴:

```text
  Inspect this repository and create a minimal binary_search.py file. Implement an iterative binary_search(nums, target) function for a sorted list of integers. Return the index if the target exists and -1 if it does not. Keep the code very small.
```

agent 完成后,在另一个终端或编辑器里检查结果。内容如下所示:

<img src="https://sebastianraschka.com/images/github/mini-coding-agent/2.webp" width="200px">

&nbsp;
### 4. 让它修改实现

接下来做一个小改动。回到 agent REPL,粘贴:

```text
Update binary_search.py so it raises ValueError if the input list is not sorted in ascending order. Keep the implementation simple.
```

再检查一次文件:

<img src="https://sebastianraschka.com/images/github/mini-coding-agent/3.webp" width="300px">

&nbsp;
### 5. 让它添加单元测试

回到 REPL,粘贴:

```text
Create test_binary_search.py with pytest tests for found, missing, empty list, first element, last element, and unsorted input raising ValueError. Keep the tests small and readable.
```

检查新生成的测试文件:

<img src="https://sebastianraschka.com/images/github/mini-coding-agent/4.webp" width="250px">

&nbsp;
### 6. 让它运行测试

回到 REPL,粘贴:

```text
Run pytest for this repo. If any test fails, fix the code or tests and rerun until everything passes.
```

你也可以在另一个终端窗口手动验证:

```
python -m pytest tmp/binary-search-repo
```

&nbsp;
### 7. 检查仓库最终状态

看看改了什么:

```bash
cd mini-coding-agent-go
cd ./tmp/binary-search-repo
git status --short
```

此时你应该有:

- `README.md`
- `binary_search.py`
- `test_binary_search.py`

&nbsp;
## 示例 2:在真实数据上对比排序算法

这个示例构建一个小型基准测试应用:从数据文件读取整数,把两种排序算法各跑若
干次,输出单次耗时报告,并逐元素比对两个算法的结果是否一致。它会用到多步任务
prompt 和 `run_shell` 工具,并且比示例 1 更依赖模型的指令遵循能力。

&nbsp;
### 1. 创建全新仓库并复制数据文件

本示例排序用的整数不是让 agent 生成的——先从本仓库的 `data/` 目录把两个数据
文件复制到目标仓库:

```bash
cd mini-coding-agent-go
mkdir -p ./tmp/sort-benchmark-repo
cd ./tmp/sort-benchmark-repo
git init
cp ../../data/data.txt ../../data/data_lite.txt .
```

复制的两个文件:

- `data.txt` —— 100,000 个带符号整数,每行一个(可能有重复)
- `data_lite.txt` —— 2,000 个带符号整数,是给本示例用的精简集

为什么是两个文件:冒泡排序是 O(n²),在完整的 `data.txt` 上不可行——10 万个整数
的冒泡排序约需要百亿量级的比较次数,远超 agent 步数预算内能跑完的时间。
`data_lite.txt` 是给本示例使用的,让冒泡排序的对比也能很快完成。如果你只做
quick_sort 版本的基准测试,可以直接使用完整的 `data.txt`。

&nbsp;
### 2. 启动 agent

这种多步任务 prompt 需要指令遵循能力较强的模型,而且基准测试会反复执行 shell
命令,所以本示例用 `--approval auto` 运行(模型仍是默认的 `gemma4:cloud`):

```bash
cd mini-coding-agent-go
./mini-coding-agent-go \
  --cwd ./tmp/sort-benchmark-repo \
  --approval auto
```

`--approval auto` 是为了跑基准方便——注意它意味着模型执行任意命令、写文件都
不再询问。

&nbsp;
### 3. 让它构建基准测试

在 `mini-coding-agent>` 提示符下粘贴(REPL 只接受单行输入,所以 prompt 用单段英文给出):

```text
Create sort_algorithms.py that reads all integers (dups allowed) from data_lite.txt in the current dir, runs bubble sort and quicksort 5 times each, reports per-run times plus the average for each, then compares the last results of the two algorithms element-by-element and prints any differing indices along with the values at those indices.
```

(如果只做 quick_sort 版本,把 prompt 里的 `data_lite.txt` 换成完整的 `data.txt` 即可。)

&nbsp;
### 4. 让它执行程序

文件生成后,回到 REPL 要求 agent 执行它:

```text
help me execute this python program
```

agent 通过 `run_shell` 运行程序并输出基准报告。以下是在 `data_lite.txt` 上的
一次真实运行的输出:

```text
Running Bubble Sort 5 times...
  Run 1: 0.077506s
  Run 2: 0.070776s
  Run 3: 0.069533s
  Run 4: 0.070005s
  Run 5: 0.070240s
Average Bubble Sort time: 0.071612s

Running Quicksort 5 times...
  Run 1: 0.001285s
  Run 2: 0.001218s
  Run 3: 0.001223s
  Run 4: 0.001182s
  Run 5: 0.001161s
Average Quicksort time: 0.001214s

Both algorithms produced identical results.
```

&nbsp;
### 5. 检查仓库最终状态

看看改了什么,并手动重跑一次基准:

```bash
cd mini-coding-agent-go
cd ./tmp/sort-benchmark-repo
git status --short
python sort_algorithms.py
```

你应该能看到 `sort_algorithms.py` 和复制过来的两个数据文件;手动重跑会输出同样
的报告。

&nbsp;
## 实用的交互命令

agent 运行期间,以下命令可用:

- `/help` 显示可用斜杠命令及各自作用。
- `/memory` 打印当前会话的提炼工作记忆。
- `/session` 显示会话 JSON 文件在磁盘上的保存路径。
- `/reset` 清空当前对话历史与工作记忆。
- `/exit` 退出交互式 agent。
