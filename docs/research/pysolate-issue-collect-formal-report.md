> **Repository status (2026-08-27)**
>
> - **Status:** Proposed.
> - **Qualifier:** Paper-level proof sketch and design reserve. This is not a claim about the current implementation, not a machine-checked proof, and not yet thesis/report/defence text.
> - **Current project scope:** Gate Contract in the [Unified Split-Phase Execution Roadmap](../plans/2026-08-27-unified-split-phase-execution-roadmap.md) may use the conservative V1 model: earliest policy-legal `issue`, `collect` at the original logical call, one synchronous sealed-source execution and one Run-private Host attempt table. Collect sinking, cross-path speculation and prepare/commit effects remain deferred.
> - **Provenance:** The report body below is preserved verbatim from the user-provided draft; only this repository-status block was added.
>
> **Proof obligations before manuscript use**
>
> 1. Separate semantic observations from real physical/economic/provider observations. A network pre-issue can affect billing, quotas, logs, observation time and contention even when it is silent to Guest Python.
> 2. Prove or operationally specify factorisation for each admitted operation class rather than treating `collect(issue(a)) ≈ H(a)` as both the principal assumption and the conclusion. Validation or retry at collect does not erase an earlier provider interaction.
> 3. Define pre-seal candidate identity and seal-time adoption separately from dynamic logical invocation identity, especially for branches, loops, source revision and discarded source.
> 4. Restrict the first theorem to finite normal or exceptional executions, or add explicit divergence and cancellation semantics before claiming coverage of loops, recursion and cancellation.
> 5. Bind authority and policy from Host-owned Run context. Compiler-emitted identifiers may select a site, but Guest-provided `policy_token`, tool identity or fingerprints must not grant authority.
>
---

# Pysolate 中 `issue` 左移与 `collect` 右移的形式化模型、正确性论证与实现方案

## 摘要

Pysolate 可以在不把同步 Python 改写成 `async`、不引入 Python 侧 scheduler、也不让 Host 执行 Python 的前提下，把一个 Host 调用拆成两个阶段：

```python
handle = issue(operation, arguments)   # 非阻塞：开始物理工作
value  = collect(handle)               # 同步：需要时等待并取得普通 Python 值
```

优化目标是：

\[
t_{\mathrm{issue}} \rightarrow \text{尽可能靠左},
\qquad
t_{\mathrm{collect}} \rightarrow \text{尽可能靠右}.
\]

但这两个移动不是无条件成立的。

- `issue` 可以左移，当且仅当参数可以安全提前固定、提前物理工作不产生过早可观察效果，并且跨越控制流时满足安全 speculation 契约。
- `collect` 可以右移，当且仅当它跨越的代码与该调用的结果、异常和逻辑效果可交换；否则 `collect` 必须停在原调用位置或更早的语义屏障前。
- 对任意 Python 和任意外部工具，不存在一个无条件正确的通用变换。正确性定理必须相对于一个明确的 **Pysolate admitted subset** 和一个明确的 **stageable Host-operation contract**。

最稳健的落地路线是：

1. **V1：只做 `issue` 左移，`collect` 保持在原调用位置。**
2. **V2：只对 pure、total、immutable、stable 的 Host 值做受限 `collect` 右移。**
3. **V3：对只读分支做预算受控的 speculative issue。**
4. **V4：对写操作使用真正的 prepare/commit 协议，而不是直接提前执行副作用。**

该模型保留以下核心不变量：

> Python 的控制流和普通计算只在 sealed source 上由一次真实的同步 CPython 执行决定；Host 只提前执行满足契约的物理工作，并在逻辑需要结果的位置同步交付。

---

# 1. 问题定义

考虑同步 Python：

```python
a = tool_A()        # 10s
b = tool_B()        # 20s
c = a + b
d = tool_D()        # 20s
return c + d
```

直接执行时，调用顺序受 Python 顺序语义约束，可能接近：

\[
10 + 20 + 20 = 50\text{s}.
\]

若三个调用都满足提前执行条件，可以变换为：

```python
_hA = issue(A)
_hB = issue(B)
_hD = issue(D)

a = collect(_hA)
b = collect(_hB)
c = a + b
d = collect(_hD)

return c + d
```

此时 Python 仍然完全同步。只有 Host 后台同时运行三个请求。理想时间约为：

\[
\max(10,20,20)=20\text{s}.
\]

真正困难的是：

- 调用位于 `if`、`while`、`try`、循环或短路表达式中；
- 参数依赖运行时 Python 中间值；
- 调用会失败、产生副作用或读取变化中的外部状态；
- Python 代码可以观察局部变量、异常顺序、时间或对象身份；
- 同一静态调用点会在循环中产生多个动态实例。

因此，正确性不能仅靠“这个调用看起来是只读的”来保证，而要把可观察语义和工具契约写清楚。

---

# 2. 三个时间点

对原程序中的每个动态 Host 调用实例 \(i\)，定义：

\[
t_i^d = \text{调用在 source/AST 中被发现的时刻},
\]

\[
t_i^I = \text{Host 物理工作真正开始的时刻，即 issue time},
\]

\[
t_i^C = \text{同步 Python 必须取得该结果的时刻，即 collect time}.
\]

一般有：

\[
t_i^d \le t_i^I \le t_i^C.
\]

优化试图增大：

\[
\Delta_i = t_i^C - t_i^I,
\]

即物理工作可以隐藏的时间窗口。

若调用物理耗时为 \(L_i\)，在无资源争用的理想模型下，该调用造成的剩余同步等待为：

\[
W_i = \max(0, t_i^I + L_i - t_i^C).
\]

因此：

- `issue` 越早，\(W_i\) 不增；
- `collect` 越晚，\(W_i\) 不增；
- 但只有在语义安全条件成立时才能移动。

这个式子说明了性能收益，但不构成正确性证明。正确性要从可观察语义出发。

---

# 3. 可观察语义

## 3.1 状态

使用三个状态空间：

\[
\sigma \in \Sigma
\]

表示 Guest Python 状态，包括局部变量、堆对象和控制栈；

\[
\omega \in \Omega
\]

表示逻辑上可观察的外部状态，例如文件、数据库、Git 仓库、邮件发送状态和公开工具效果；

\[
\pi \in \Pi
\]

表示 Host 私有的物理准备状态，例如 pending request、缓存项、网络连接、未发布结果和内部 handle 表。

`issue` 允许改变 \(\pi\)，但在被证明安全的模型中，不得提前改变 Guest 可观察的 \(\sigma\) 或逻辑外部状态 \(\omega\)。

## 3.2 观察结果

定义程序观察：

\[
\operatorname{Obs}(P,\sigma_0,\omega_0)
=
(\operatorname{outcome}, \tau),
\]

其中：

- `outcome` 是正常返回值或未捕获异常；
- \(\tau\) 是 Guest 可观察的逻辑效果序列；
- 物理耗时、Host 内部请求日志和私有缓存命中不计入 \(\tau\)。

因此这里证明的是：

> **功能结果、Python 异常行为和逻辑外部效果相同，忽略物理执行时间和 Host 私有调度事件。**

如果程序被允许直接读取真实墙钟、使用信号、并发线程、`sys.settrace()` 或 frame introspection，那么任何调度优化都可能被观察到。此类能力必须被禁止、虚拟化，或当成优化屏障。

---

# 4. 原始 Host 调用语义

一个普通同步 Host 操作 \(H\) 可抽象为：

\[
H(a,\omega)
\Downarrow
(o,\omega',\tau_H),
\]

其中：

- \(a\) 是已经按 Python 语义求值得到的参数；
- \(o\) 是 `return(v)` 或 `raise(e)`；
- \(\omega'\) 是新的逻辑外部状态；
- \(\tau_H\) 是逻辑可观察效果。

原程序在调用点：

1. 按 Python 左到右规则求值参数；
2. 同步执行 \(H\)；
3. 在该位置返回或抛异常；
4. 再继续后续 Python。

---

# 5. Split-phase Host 契约

一个操作只有满足 split-phase 契约，才能进入提前执行集合 \(T\)。

对操作 \(H\)，要求存在：

```text
issue_H
collect_H
discard_H
```

满足下列性质。

## 5.1 Silent issue

\[
\operatorname{issue}_H(a,\pi)
\rightarrow
(k,\pi')
\]

只返回一个不可伪造的 handle \(k\)，允许改变 Host 私有状态 \(\pi\)，但：

\[
\sigma'=\sigma,\qquad \omega'=\omega,\qquad \tau=\epsilon.
\]

即 `issue` 对 Guest 语义是静默的。

现实中它可以发出网络请求；但只有当该请求在抽象语义中属于“私有准备”，不会提前产生 Guest 有权观察的效果时，才满足本条件。

## 5.2 Factorisation

对所有合法参数和逻辑外部状态：

\[
\operatorname{collect}_H(
    \operatorname{issue}_H(a),
    \omega
)
\approx
H(a,\omega).
\]

这里的 \(\approx\) 表示：

- 返回值或异常相同；
- 逻辑外部状态变化相同；
- 逻辑效果 trace 相同。

这是整套正确性的核心契约。

它允许 `collect` 在必要时：

- 使用已经准备好的成功结果；
- 验证结果仍然有效；
- 对过期或不稳定结果重新执行；
- 把 staged write 在此刻真正 commit；
- 对 tentative failure 在逻辑调用点重新确认。

因此，“提前请求已经失败”不一定必须在 `collect` 时直接抛出；对于瞬时失败，Host 可以在逻辑调用点重试，以维持原始语义。

## 5.3 Silent discard

如果控制流最终没有到达该调用：

\[
\operatorname{discard}_H(k,\pi)
\rightarrow \pi'
\]

必须满足：

\[
\omega'=\omega,\qquad \tau=\epsilon.
\]

即未消费的 speculative work 可以取消或丢弃，不产生逻辑效果。

## 5.4 Run isolation

handle 必须绑定：

```text
logical run
capability identity
authority scope
tool identity
normalised argument digest
dynamic occurrence identity
```

不同逻辑 run 不得通过 handle、可变结果对象或错误状态互相泄漏。

## 5.5 Deferred failure

`issue` 不得因为普通 provider failure 而在提前位置向 Python 抛异常。它只能把成功或失败记录在 handle 中；异常在 `collect` 的语义位置才可见。

---

# 6. 哪些操作满足这个契约

可以把工具分成四类。

## 6.1 Immutable deterministic read

例如：

```text
按 content hash 读取对象
读取固定版本数据集
读取 commit hash 指定的 Git tree
按不可变 key 读取模型输出缓存
```

只要结果由参数唯一确定，可以直接提前得到最终结果。

## 6.2 Validated read

例如变化中的远程查询。`issue` 先取得候选结果；`collect`：

- 验证版本、ETag、事务快照或 freshness token；
- 若仍有效，直接使用；
- 若无效，在逻辑调用点重新读。

这仍可满足 factorisation，但收益取决于验证命中率。

## 6.3 Prepare/commit effect

例如：

```text
准备 patch
构造邮件内容
上传临时对象
预计算数据库写集
```

`issue` 只能做不可见准备；真正的发布、发送、覆盖或 commit 必须在原逻辑效果位置完成。

## 6.4 Non-stageable operation

若工具：

- 调用即产生不可撤销效果；
- 结果对真实调用时刻敏感且不可验证；
- 会改变后续权限、配额或外部可见状态；
- 无法安全取消或丢弃；

则：

\[
T(H)=\text{false}.
\]

它必须保持普通同步调用。

---

# 7. 参数提前固定的正确性

即使 Host 操作本身可提前，参数表达式也不能随便前移。

原调用：

```python
x = H(e)
```

若要在更早位置 \(p\) 发出：

```python
a = capture(e)
k = issue(H, a)
```

必须满足 `CaptureSafe(e,p,c)`，其中 \(c\) 是原调用点。

要求：

1. 在所有最终到达 \(c\) 的路径上，\(e\) 在 \(p\) 已经可求值；
2. 在 \(p\) 求值 \(e\) 不产生可观察副作用或异常；
3. \(p\) 到 \(c\) 之间没有写操作使参数值改变；
4. 序列化后的参数与在 \(c\) 求值得到的参数一致；
5. 参数不包含随后会被原位修改的共享可变对象，除非 issue 时使用的正是原语义要求的快照；
6. 参数求值不依赖被移动跨越的动态 authority、cwd、环境变量、事务或 context manager 状态。

因此以下通常安全：

```python
H("literal")
H(local_single_assignment)
H(immutable_record.field)
```

以下默认不安全：

```python
H(obj.pop())
H(mapping[key])          # 任意 __getitem__ 可能有副作用
H(mutable_list)
H(time.time())
H(next(iterator))
H(global_name)           # 若中间可能重绑定
```

实现上不需要证明任意 Python 表达式纯。遇到未知情况，保守地不前移。

---

# 8. 控制流与 speculation

设调用点 \(c\) 位于条件路径 \(G\) 中。

## 8.1 Same-path issue

若 issue 点 \(p\) 与 \(c\) 控制等价，即到达 \(p\) 就必然到达 \(c\)，可不视为 speculation。

CFG 上的一个强充分条件是：

- \(p\) 支配 \(c\)；
- 在当前控制 region 内，\(c\) 后支配 \(p\)。

这意味着提前 issue 不会在原调用不发生的路径上额外执行。

## 8.2 Speculative issue

若 \(p\) 可能执行而 \(c\) 最终不执行，则必须满足：

\[
\operatorname{SpecSafe}(H,a)=\text{true}.
\]

至少要求：

- `discard` 静默；
- 无不可见之外的副作用；
- 资源浪费在预算内；
- 调用不消耗程序可观察的单次 token、nonce 或配额；
- 不改变其他真实调用的结果；
- 未消费的错误不会泄漏。

分支不由 Host 判断。Host 只可能提前做两个候选工作；最终 CPython 正常决定进入哪个分支，并只 collect 实际路径的 handle。

---

# 9. `collect` 为什么不能无条件右移

考虑：

```python
a = H()          # H 可能失败
send_email()
return a
```

若改成：

```python
_h = issue(H)
send_email()
a = collect(_h)
return a
```

当 \(H\) 失败时，原程序不会发送邮件，而变换后会发送。语义不同。

再考虑：

```python
a = H()
print("done")
return a
```

即使 `H` 无副作用，但会抛异常，移动也会改变输出和异常顺序。

因此“collect 尽量右移”必须解释为：

> **collect 只能跨越与它可交换的透明代码。**

---

# 10. Collect commutation 条件

设 `C` 表示 `collect(k)`，`S` 是其后的一条语句。若对所有相关状态都有：

\[
\operatorname{Obs}(C;S)
=
\operatorname{Obs}(S;C),
\]

且最终 Guest 状态和逻辑外部状态等价，则称：

\[
C \bowtie S.
\]

若：

\[
C \bowtie S_1,\quad
C \bowtie S_2,\quad \dots,\quad
C \bowtie S_n,
\]

则可通过逐步交换，把 `collect` 从原位置向右移动到 \(S_n\) 之后。

编译器不可能直接判定一般的语义可交换性，因此采用保守的充分条件。

## 10.1 允许跨越的典型条件

至少满足：

1. `S` 不读取 Host 结果变量；
2. `S` 不通过 `locals()`、frame、closure、debugger 或 reflection 观察该绑定是否已经存在；
3. `S` 不改变 `collect` 结果有效性所依赖的外部状态；
4. `S` 不改变 capability、事务、cwd、环境或授权上下文；
5. `collect` 在该工具契约下是 pure、total、stable：
   - 不产生逻辑效果；
   - 不会抛异常；
   - 返回不可变序列化值；
6. 或者，虽然 `collect` 可能失败/有效果，但 `S` 本身是无效果、不会失败、且与其完全独立的纯计算；
7. 不跨越异常处理 region、`finally`、`with`、`yield`、线程或取消边界；
8. 不跨越时间、随机、信号等可观察调度的操作。

## 10.2 实际建议

- 对普通可能失败的 tool call：`collect` 保持在原调用位置。
- 对 `PURE_TOTAL_IMMUTABLE` 的结果：允许移动到第一次严格使用之前。
- 对 staged write：commit 必须保持在原逻辑效果位置；物理结果的反序列化可稍后，但收益通常有限。

---

# 11. Python 局部变量绑定问题

Python 可以通过以下方式观察变量绑定时机：

```python
locals()
inspect.currentframe()
sys.settrace()
closure cell
eval()
exec()
```

原程序：

```python
a = H()
S
use(a)
```

若变换为：

```python
_h = issue(H)
S
a = collect(_h)
use(a)
```

则在 `S` 中，`a` 尚未绑定。这对完整 Python 语义并不等价。

因此，`collect` 右移定理必须相对于一个 admitted subset：

```text
无 frame/locals introspection
无动态 eval/exec
无 line tracing/debug hooks
无并发线程读取局部状态
无信号处理器观察中间状态
```

在 Pysolate 的受限生成代码环境中，这通常是合理限制；但报告和实现必须明确写出，而不能声称对任意 CPython 程序透明。

另一条更保守的路线是：

- V1 不移动 collect；
- V2 仅在编译器能证明变量在区间内不可观察时移动。

---

# 12. 正确性引理

以下是 paper-level proof sketch，而非机器检查证明。

## 引理 1：Silent issue insertion

设原程序为：

\[
P = A; H(a); B.
\]

在 \(A\) 内某个安全位置插入：

\[
k=\operatorname{issue}_H(a)
\]

得到：

\[
P_1 = A_1; k=\operatorname{issue}_H(a); A_2; H(a); B.
\]

若：

- 参数 capture safe；
- `issue` silent；
- handle 私有且不被 Guest 观察；

则：

\[
P \approx P_1
\]

直到原调用点之前的所有 Guest 状态、逻辑外部状态和观察 trace 相同。

**证明。**

`issue` 只改变 Host 私有状态 \(\pi\)，且产生空 trace。普通 Python 后续步骤不读取 \(\pi\)。因此可构造一步弱模拟：原程序跳过该私有步骤后，与变换程序处于相同的 \((\sigma,\omega)\)。证毕。

---

## 引理 2：Split replacement

把原调用：

\[
H(a)
\]

替换为：

\[
\operatorname{collect}_H(k)
\]

其中 \(k\) 来自对应的 `issue_H(a)`。

由 factorisation：

\[
\operatorname{collect}_H(
    \operatorname{issue}_H(a),
    \omega
)
\approx
H(a,\omega).
\]

因此在相同逻辑调用状态下：

- 返回值相同；
- 异常相同；
- 逻辑效果相同；
- 后续 Python 获得相同状态。

证毕。

---

## 引理 3：安全 speculation

若提前 issue 位于最终未进入的控制路径之外，而：

- `issue` silent；
- `discard` silent；
- handle 未被 collect；
- speculative work 不改变其他真实调用结果；

则该额外工作只改变 \(\pi\)，不改变 \((\sigma,\omega,\tau)\)。

因此未走到原调用点的执行与原程序观察等价。证毕。

---

## 引理 4：单步 collect sinking

若：

\[
C \bowtie S,
\]

则：

\[
C;S \approx S;C.
\]

这是 commutation 定义的直接结果。

---

## 引理 5：多步 collect sinking

若：

\[
C \bowtie S_i
\quad
\forall i\in[1,n],
\]

且每次交换后条件仍成立，则：

\[
C;S_1;\dots;S_n
\approx
S_1;\dots;S_n;C.
\]

**证明。**

对 \(n\) 归纳。

- \(n=0\) 显然；
- 假设对 \(n\) 成立，由单步引理将 \(C\) 与 \(S_{n+1}\) 交换，得到 \(n+1\) 情况。证毕。

---

# 13. 全局正确性定理

## 定理

设程序 \(P\) 属于 Pysolate admitted subset。对每个被变换的动态 Host 调用实例，满足：

1. Host operation 的 split-phase factorisation；
2. issue 参数 capture safe；
3. issue placement 满足 same-path 或 SpecSafe；
4. handle 按 run、authority、参数和动态 occurrence 隔离；
5. failure 只在 collect 暴露；
6. collect 只跨越与其可交换的语句；
7. AST rewrite 保持普通 Python 表达式的求值顺序；
8. 未匹配或不安全情况回退为原位同步调用。

则对所有合法初始状态：

\[
\operatorname{Obs}(P,\sigma_0,\omega_0)
=
\operatorname{Obs}(T(P),\sigma_0,\omega_0).
\]

## 证明思路

变换可分解为有限次局部重写：

1. 插入 silent issue；
2. 用 matching collect 替换原调用；
3. 可选地跨越若干可交换语句移动 collect；
4. 在未采用的 speculative 路径上 silent discard。

由引理 1–5，每个局部重写保持观察等价。观察等价具有传递性，因此整个变换保持观察等价。

对循环和递归，按动态调用实例归纳。每次迭代或递归 activation 使用不同 occurrence identity，局部引理分别适用，故任意有限执行前缀保持等价。对正常终止、异常终止和取消路径分别应用相同论证。证毕。

---

# 14. 这个证明没有覆盖什么

不能据此声称对“任意 Python、任意工具”完全透明。以下情况必须禁止、虚拟化或形成 barrier：

- `locals()`、frame inspection、`eval`、`exec`、trace hooks；
- Guest 多线程、信号处理器和共享局部状态；
- 真实墙钟、随机数、provider freshness 对调用时刻敏感；
- 可变参数在 issue 与原调用点之间发生变化；
- 任意 `__getattr__`、`__getitem__`、descriptor 或 operator overload 被提前求值；
- 动态 import、context manager、事务或 cwd 改变工具语义；
- 不可撤销外部副作用；
- 未受控的 quotas、nonce、rate-limit 和 one-shot capability；
- generator/yield 边界；
- 无法延迟或重放的 provider failure；
- 由资源争用导致一个提前请求改变另一个请求结果。

遇到这些情况，正确实现必须保守退化，而不是猜测。

---

# 15. 编译器实现

## 15.1 总体 pipeline

```text
sealed source
    ↓
parse AST
    ↓
识别 Host capability calls
    ↓
A-normal form / evaluation-order normalisation
    ↓
CFG + control dependence + exception regions
    ↓
def-use / alias / effect facts
    ↓
issue placement
    ↓
collect placement
    ↓
AST rewrite
    ↓
同步 CPython 执行
```

Streaming 阶段可在完整 source seal 前做一个更保守的前缀分析，只 predispatch 参数完全 source-known 的调用。最终 AST pass 负责验证和匹配。

---

## 15.2 A-normal form

复杂表达式必须先拆开，以保持 Python 左到右求值顺序。

原代码：

```python
z = f(g(), tool(x), h())
```

不能直接随意把 `tool(x)` 提到最前面，因为：

- `g()` 可能有副作用或抛异常；
- Python 参数按左到右求值；
- `h()` 是否执行取决于前面是否成功。

保守 lowering：

```python
_t0 = g()
_t1 = tool(x)
_t2 = h()
z = f(_t0, _t1, _t2)
```

然后只对 `_t1 = tool(x)` 做 split，并且 issue 不能跨越 `_t0 = g()`，除非编译器证明它们可交换。

---

## 15.3 Call-site descriptor

每个静态 Host 调用点生成：

```text
CallSite {
    site_id
    source_span
    structural_hash
    tool_id
    argument_expression
    control_region
    exception_region
    loop_region
    authority_context
    tool_contract
}
```

`site_id` 不足以表示循环中的动态实例，因此运行时 handle key 还要包括：

```text
occurrence_id
```

它可以是：

```text
(site_id, frame_activation_id, loop_iteration_sequence)
```

或者由 runtime 为每次执行该 issue 节点分配单调序号。

---

## 15.4 Issue placement

对调用点 \(c\)，从原位置开始向 CFG 前驱方向搜索最早合法位置 \(p\)。

候选点必须满足：

```text
ArgumentsAvailable(p)
CaptureSafe(p, c)
AuthorityStable(p, c)
ExceptionSafeForCapture(p, c)
ControlEquivalent(p, c) OR SpecSafe(tool)
LoopOccurrencePreserved(p, c)
NoForbiddenBarrier(p, c)
```

推荐算法：

```python
p = original_call_point

while predecessor exists:
    if can_hoist_issue_across(predecessor_statement, call_site):
        p = predecessor_position
    else:
        break
```

跨基本块时：

- 非 speculative hoist 要求控制等价；
- speculative hoist 要求工具标记允许；
- 默认不跨循环头、`try`、`with` 和 authority region。

---

## 15.5 Collect placement

V1：

```text
collect 保持在原调用位置。
```

这是最容易严格证明、也最符合现有 Pysolate 的版本。

V2 对满足：

```text
PURE
TOTAL
IMMUTABLE_RESULT
STABLE
NO_REFLECTION
```

的调用向后搜索。

停止条件包括：

```text
第一次真正使用结果
可能观察变量绑定
可能抛异常的效果语句
逻辑外部效果
try/except/finally 边界
with/transaction/authority 边界
loop/yield 边界
time/random/signal barrier
frame exit / return
```

多分支使用时，可在各路径的第一次使用前插入：

```python
value = collect_once(handle)
```

`collect_once` 只是同步的幂等取值函数，不是 scheduler：

```text
若未完成：阻塞
若完成：返回缓存结果
若已 collect：返回同一个 immutable value
若失败：在当前位置抛出保存的异常
```

为了避免 Python 绑定可观察问题，V2 仅作用于已证明无 reflection 的局部 SSA value。

---

# 16. AST rewrite ABI

建议内部 ABI：

```python
handle = __pysolate_issue__(
    site_token,
    tool_id,
    frozen_args,
    policy_token,
)

value = __pysolate_collect__(
    handle,
    site_token,
)

__pysolate_discard__(handle)
```

Streaming predispatch 对应：

```python
handle = __pysolate_issue_or_reuse__(
    site_token,
    request_fingerprint,
    tool_id,
    frozen_args,
    policy_token,
)
```

运行时行为：

```text
若找到同一 run 中匹配的 predispatch:
    返回已有 handle
否则:
    现在创建新 job
```

匹配必须检查：

```text
tool identity
normalised arguments
capability scope
logical run
source seal identity
call-site fingerprint
speculation policy
```

不能只凭 source line number 复用。

---

# 17. Host 状态机

建议每个 job 使用：

```text
CREATED
  ↓
ISSUED
  ↓
RUNNING
  ├── SUCCEEDED
  ├── TENTATIVE_FAILED
  └── CANCELLED
```

collect 后：

```text
SUCCEEDED
  ↓
VALIDATING / COMMITTING
  ├── COLLECTED
  └── FAILED_AT_COLLECT
```

完整字段：

```text
Job {
    handle_id
    logical_run_id
    source_seal_id
    site_id
    occurrence_id

    tool_id
    authority_token
    request_fingerprint
    argument_blob

    stage_mode
    speculation_mode
    failure_policy
    timeout_policy

    state
    result_blob
    tentative_error
    validation_token

    created_at
    issued_at
    completed_at

    owner
    refcount
    cancellation_token
}
```

---

# 18. Host tool metadata

```text
ToolContract {
    effect_class:
        IMMUTABLE_READ
        VALIDATED_READ
        PREPARE_COMMIT
        NON_STAGEABLE

    argument_mode:
        FROZEN_VALUE
        VERSIONED_REFERENCE

    result_mode:
        IMMUTABLE_SERIALISED
        PRIVATE_COPY
        COW_MAPPING

    speculation:
        NEVER
        SAME_PATH_ONLY
        BUDGETED

    failure_policy:
        REUSE_STABLE_FAILURE
        RETRY_AT_COLLECT
        VALIDATE_AT_COLLECT

    authority_policy:
        BIND_AT_ISSUE
        RECHECK_AT_COLLECT

    timeout_policy:
        PHYSICAL_PREP_TIMEOUT
        LOGICAL_CALL_TIMEOUT
}
```

尤其要分开：

- preparation timeout；
- logical call timeout。

否则提前开始可能改变原程序本应观察到的超时行为。

---

# 19. 复杂控制流的运行方式

考虑：

```python
user = get_user("alice")

if user["premium"]:
    recs = search(user["topic"])
    score = normalize(user["score"])

    if score > 10:
        quote = price(recs[0]["sku"])
    else:
        quote = fallback("premium")
else:
    quote = fallback("basic")

tax = tax_table("UK")
return render(quote, tax)
```

在 streaming 阶段：

```text
get_user("alice")       可 preissue
tax_table("UK")         可 preissue
fallback("premium")     若 SpecSafe 可 preissue
fallback("basic")       若 SpecSafe 可 preissue

search(user["topic"])   参数 runtime-only，不能发
price(recs[0]["sku"])   参数和 branch runtime-only，不能发
```

sealed 后同步 CPython：

```python
_h_user = issue_or_reuse(GET_USER)
user = collect(_h_user)

if user["premium"]:
    _h_search = issue(search, user["topic"])

    score = normalize(user["score"])

    recs = collect(_h_search)

    if score > 10:
        _h_price = issue(price, recs[0]["sku"])
        quote = collect(_h_price)
    else:
        _h_fb = issue_or_reuse(FALLBACK_PREMIUM)
        quote = collect(_h_fb)
else:
    _h_fb = issue_or_reuse(FALLBACK_BASIC)
    quote = collect(_h_fb)

_h_tax = issue_or_reuse(TAX)
tax = collect(_h_tax)

return render(quote, tax)
```

没有 Python scheduler。

运行自然形成多个同步 wave：

```text
source-time issue wave
    ↓
collect user
    ↓
CPython 决定 branch
    ↓
runtime issue search
    ↓
CPython 做独立 normalize
    ↓
collect search
    ↓
CPython 决定内层 branch
    ↓
runtime issue price 或复用 fallback
    ↓
collect
```

中间状态和控制流仍完全由 CPython 决定。

---

# 20. 循环

原代码：

```python
for user in users:
    profile = fetch_profile(user.id)
    consume(profile)
```

动态调用实例必须区分每次迭代。

保守变换：

```python
for user in users:
    _h = issue(
        site=PROFILE_SITE,
        occurrence=next_occurrence(),
        args=(user.id,),
    )
    profile = collect(_h)
    consume(profile)
```

若 loop body 中有独立计算：

```python
for user in users:
    profile = fetch_profile(user.id)
    x = local_cpu_work(user)
    consume(profile, x)
```

则：

```python
for user in users:
    _h = issue(fetch_profile, user.id)
    x = local_cpu_work(user)
    profile = collect(_h)
    consume(profile, x)
```

若希望跨迭代批量并发，需要更激进的 loop transformation：

```python
handles = []
for user in users:
    handles.append(issue(fetch_profile, user.id))

for user, handle in zip(users, handles):
    profile = collect(handle)
    consume(profile)
```

这会改变局部变量绑定、异常时机和资源使用，必须单独证明，不应混入第一版通用 pass。

---

# 21. `try/except/finally`

原代码：

```python
try:
    x = tool()
    S
except ToolError:
    recover()
```

若 `tool` 可能失败，`collect` 必须处于原 exception region 内。不能移动到 `try` 外：

```python
_h = issue(tool)

try:
    x = collect(_h)
    S
except ToolError:
    recover()
```

`issue` 可以在 `try` 前，只要：

- 参数 capture 不会抛异常；
- issue 本身不向 Python 抛普通 provider error；
- 未进入 try 的异常处理语义不受影响。

`finally`、context manager 和事务边界一律作为强 barrier，除非有专门证明。

---

# 22. Authority-preserving 条件

提前物理工作不能绕过逻辑授权。

建议：

1. issue 时绑定 capability identity 和 run owner；
2. 对只读 immutable work，可在 issue 时做授权；
3. 对会 commit 的操作，在 collect/commit 时重新检查 authority；
4. capability 在中途被 revoke 时，未 commit 的 work 必须不可发布；
5. speculative result 不能自动进入 Guest VFS、Git tree 或共享 cache；
6. collect 时只向当前逻辑 run 返回 private copy 或 COW view；
7. cache key 必须包含所有影响权限和语义的上下文。

这样可以维持：

> physical work may be shared or started early; logical authority, publication and mutable state remain per run.

---

# 23. 失败和取消

## 23.1 Tentative failure

若 source-time provider 请求失败：

```text
DNS failure
transient HTTP 500
temporary rate limit
```

不能自动等价于原调用点失败。

工具契约可以规定：

```text
issue failure -> TENTATIVE_FAILED
collect -> retry or validate
```

只有稳定失败，例如参数 schema 永久无效，才可直接复用。

## 23.2 Branch not taken

若 speculative branch 未进入：

```text
discard(handle)
```

Host：

- 尽量取消；
- 不能取消则完成后丢弃；
- 不发布结果；
- 不向 Python暴露错误；
- 计入内部 speculation budget。

## 23.3 Run cancellation

逻辑 run 取消时：

- 所有未 collect job 标记 orphan/cancelled；
- prepared result 不得跨 run 泄漏；
- 可共享的 immutable backing 只能通过独立 cache policy 保留；
- 外部副作用若未 commit 不得发布。

---

# 24. 静态验证器

AST rewrite 后运行一个 verifier，检查：

```text
每个 handle 对应一个合法 site
每个 collect 的 handle 定义支配该 collect
每个普通结果使用都被 collect 支配
每个动态 occurrence 不发生错误复用
collect 不跨 exception/authority barrier
speculative issue 只用于 SpecSafe tool
参数 digest 与 issue 位置的数据流一致
未走路径的 handle 有清理路径
内部 ABI token 不可由 Guest 构造
source mapping 和 traceback mapping 完整
```

若 verifier 失败，整段程序回退到原始同步执行，而不是尝试部分运行一个未验证 rewrite。

---

# 25. 测试与验证方案

## 25.1 Differential execution

对同一程序运行：

```text
baseline synchronous interpreter
transformed issue/collect interpreter
```

比较：

```text
return value
uncaught exception type/message
Guest-visible effect trace
final VFS/Git/database state
capability audit trace
```

物理 issue 日志单独记录，不参与逻辑 trace 比较。

## 25.2 Deterministic fake Host

为每个 fake tool 配置：

```text
delay
success/failure
stable/transient failure
logical effect
validation token
cancellation behaviour
```

随机打乱完成顺序，验证语义不变。

## 25.3 Property-based AST generation

生成受限 Python core：

```text
assignment
if/else
nested branch
bounded loop
try/except
pure local expressions
Host calls
```

随机生成延迟、异常和控制条件，检查 baseline 与 transformed trace 等价。

## 25.4 Metamorphic properties

必须满足：

```text
所有 tool 标记 NON_STAGEABLE -> rewrite 与 baseline 等价
所有 delay 设为 0 -> 结果和 trace 不变
随机改变物理完成顺序 -> 逻辑 trace 不变
未进入 branch 的 speculative failure -> Python 不得看到
同一 predispatch 参数不匹配 -> 必须回退，不得错误复用
run A 的 handle -> run B collect 必须失败
```

## 25.5 Small-model checking

可以为一个极小语言实现参考状态机：

```text
Seq
If
Assign
HostCall
Raise
Try
```

枚举短程序和 Host completion interleaving，验证局部 rewrite 定理。它不能证明完整 CPython，但能验证 split-phase 核心协议和 Host 状态机。

## 25.6 Fault injection

至少覆盖：

```text
issue submission failure
provider timeout
success then validation failure
collect cancellation
double collect
discard racing with completion
run cancellation
cache eviction
worker crash/restart
duplicate predispatch
wrong source seal
authority revoked before collect
```

---

# 26. 性能命题

在下列理想假设下：

- 每个物理 job 耗时固定；
- 提前 job 不争用资源；
- scheduler 有足够并发；
- Python 本地计算耗时不因变换改变；

对任意调用 \(i\)：

\[
W_i'
=
\max(0,t_i^{I'}+L_i-t_i^{C'})
\]

若：

\[
t_i^{I'} \le t_i^I,
\qquad
t_i^{C'} \ge t_i^C,
\]

则：

\[
W_i' \le W_i.
\]

因此单调用阻塞时间不增加。

但实际系统中，speculation 可能引入：

- provider 并发竞争；
- rate limit；
- Host worker queue contention；
- 内存和网络开销；
- 对关键路径工作的反向干扰。

所以“全发”不是无条件最优。Host 仍需要：

```text
per-run concurrency budget
per-tool concurrency limit
priority for demanded work
speculation budget
cancellation
```

建议优先级：

```text
collect 正在等待的 demanded job
    >
当前真实路径 issue 的 job
    >
source-time same-path predispatch
    >
未知分支 speculative job
```

---

# 27. 推荐实施顺序

## Phase 0：工具契约

先实现：

```text
effect class
stage mode
speculation mode
failure policy
authority policy
timeout policy
```

没有契约就不做提前执行。

## Phase 1：最强保守基线

```text
source-time predispatch
runtime issue_or_reuse
collect 保持原调用位置
```

这一步已经可以正式证明，而且与现有 Pysolate 最接近。

## Phase 2：基本块内 issue hoisting

只跨越：

```text
无效果
无异常
不改变参数
同一 control/exception region
```

的普通 Python 语句。

## Phase 3：运行时控制流解锁

调用藏在 branch 中时，等 CPython 真正进入 branch，再在 branch 最前可用位置 issue。仍然无需 scheduler。

## Phase 4：受控 speculation

只对：

```text
immutable read
silent discard
预算允许
```

的工具跨 control dependence 提前。

## Phase 5：受限 collect sinking

只对：

```text
PURE_TOTAL_IMMUTABLE_STABLE
```

的结果，并在禁止 reflection 的局部 SSA 区间内移动。

## Phase 6：prepare/commit effects

对写操作显式实现：

```text
prepare early
commit at original logical point
```

不要把普通 side-effecting call 直接标为 speculative safe。

---

# 28. 最终设计原则

可以把系统压缩成四条规则。

## 规则一：Host 不执行 Python

Host 不计算：

```text
a + 1
obj["field"]
branch condition
loop body
```

它只执行显式 Host capability operation。

## 规则二：Python 不变成 async

Guest 代码仍是单线程同步 CPython。内部只有：

```text
issue: 快速返回 handle
collect: 同步等待
```

没有 `await`、event loop 或 continuation scheduler。

## 规则三：物理开始与逻辑观察分离

\[
\boxed{
\text{physical issue moves left}
}
\]

\[
\boxed{
\text{logical collect moves right only across transparent code}
}
\]

## 规则四：优化失败只降低性能

任何无法证明安全的情况：

```text
不 predispatch
不 hoist
不 speculate
不 sink collect
```

直接回退到原位同步调用。

因此 correctness 不依赖优化器成功。

---

# 29. 可用于论文或答辩的核心表述

> Pysolate splits an eligible Host operation into an early physical issue and a later synchronous collect. The issue may move to the earliest point at which its arguments, authority and effect contract are stable; the collect may move only across code that is observationally transparent to the operation. Python control flow and ordinary computation remain a single sealed CPython execution, while the Host manages only private pending work and delivers the result at the logical demand point.

中文：

> Pysolate 将符合条件的 Host 操作拆成提前的物理发出与稍后的同步收集。`issue` 可以移动到参数、权限与效果契约最早稳定的位置；`collect` 只能跨越对该操作观察透明的代码。Python 的控制流和普通计算仍由 sealed source 上的一次同步 CPython 执行完成，Host 只管理私有的 pending work，并在逻辑需求点交付结果。

最紧凑的公式是：

\[
\boxed{
\text{earliest safe issue}
+
\text{latest semantics-preserving collect}
}
\]

而不是：

\[
\text{execute partial Python on the Host}.
\]

---

# 30. 最终结论

这套模型确实可以被形式化证明为正确，但定理是条件式的：

\[
\text{stageable tool contract}
+
\text{safe argument capture}
+
\text{safe control placement}
+
\text{collect commutation}
+
\text{restricted observable Python}
\Rightarrow
\text{observational equivalence}.
\]

其中：

- `issue` 左移是基础能力，适用范围较广；
- `collect` 右移是更强优化，受异常、效果、变量绑定和 Python 反射语义严格限制；
- 对任意外部调用直接提前执行，没有一般正确性；
- 对任意完整 Python 声称透明，也不成立；
- 只要默认保守、工具契约明确、rewrite 可验证，并保留原位同步 fallback，这一设计是 clean、可实现且与 Pysolate 的“logical ownership / physical work separation”完全一致的。
