# Touchpad 项目计划（中文版，审查修订）

> 状态：规划阶段。仓库目前只有 `PLAN.md` 和 `docs/kb-*.md`，尚无 `go.mod`、应用源码或可运行样机；这些文件在本次审查时均为未跟踪文件。以下“可用”表示查过上游源码或平台文档，**不表示已在本项目设备上验证**。
>
> 优先平台：Android 手机、macOS 主机、Windows 主机。技术候选：Go + NSGo；Linux、iOS 放在后续阶段。

## 1. 审查结论与依据

本次查阅了 NSGo 上游提交 [`b01de331`](https://github.com/oneo-me/nsgo/tree/b01de331cb13711d715b267a50f271d0a532cd4c)、本机 `adb help`，以及下表的平台资料。原来的三份英文知识库保留作设计草稿，其中的数值、代码片段和平台能力需要按本计划重新验证，不能直接作为实现规范。

| 原计划判断 | 查证结果与修订 |
| --- | --- |
| NSGo 单工程构建与 Android 原生视图 | 上游 `Embedded.java` 支持按类名反射创建 `View`；`NsgoView.java` 的默认触摸路径未展开历史样本，`EventTouch` 的 Changed 阶段可合并。可做原生采样试验；本地修改 NSGo 的 Java 源码后必须按其 `runtime/build.sh` 重建并携带 `classes.dex`，需要固定 fork 或可重复的补丁流程，不能只在本仓库增加一个 Java 文件就指望普通依赖构建自动收录。[反射视图](https://github.com/oneo-me/nsgo/blob/b01de331cb13711d715b267a50f271d0a532cd4c/internal/platform/android/java/dev/oneo/nsgo/Embedded.java)、[触摸路径](https://github.com/oneo-me/nsgo/blob/b01de331cb13711d715b267a50f271d0a532cd4c/internal/platform/android/java/dev/oneo/nsgo/NsgoView.java)、[运行时构建脚本](https://github.com/oneo-me/nsgo/blob/b01de331cb13711d715b267a50f271d0a532cd4c/internal/platform/android/runtime/build.sh)、[事件合并](https://github.com/oneo-me/nsgo/blob/b01de331cb13711d715b267a50f271d0a532cd4c/internal/driver/events.go)。 |
| 构建命令 | 上游支持 `nsgo build -target macos_arm64,windows_x64,android_arm64` 这样的目标列表；是否能在开发机完成签名、工具链安装和全部产物构建，仍需实际运行。[命令用法](https://github.com/oneo-me/nsgo/blob/b01de331cb13711d715b267a50f271d0a532cd4c/cmd/nsgo/main.go)、[目标列表](https://github.com/oneo-me/nsgo/blob/b01de331cb13711d715b267a50f271d0a532cd4c/cmd/nsgo/build.go)。 |
| Windows 触摸注入即精确触控板 | `CreateSyntheticPointerDevice` 只提供 `PT_TOUCH` 或 `PT_PEN`，不是精确触控板设备；Windows Precision Touchpad 的实现指南要求 HID 协议。首版先实现鼠标与滚轮，以及明确映射的系统快捷键；触摸注入留给单独应用兼容性实验。[合成指针 API](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-createsyntheticpointerdevice)、[精确触控板指南](https://learn.microsoft.com/en-us/windows-hardware/design/component-guidelines/touchpad-implementation-guide)。 |
| Windows 小于 120 的滚轮增量可保证逐帧平滑 | 微小滚轮增量允许发送，但接收程序可以累计到 `WHEEL_DELTA=120` 才动作，也可以滚动部分行。实现要保留余数，逐应用测试，不能承诺所有程序的像素级滚动。[WM_MOUSEWHEEL](https://learn.microsoft.com/en-us/windows/win32/inputdev/wm-mousewheel)。 |
| 蓝牙 HID 等于 BLE GATT、且必有原生多指手势 | 查阅 Android 9 的 `BluetoothHidDevice` 源码：`registerApp` 使用 HID SDP 描述符，要求跟踪注册回调，应用离开前台可能被自动取消注册。不能把此 API 直接写成 HID over GATT UUID `0x1812`，也不能由标准鼠标报告推导出主机精确触控板能力。先验证普通鼠标、按键和滚轮；多点 HID 报告作为后续设备兼容性研究。[Android 9 API 源码](https://github.com/aosp-mirror/platform_frameworks_base/blob/android-9.0.0_r1/core/java/android/bluetooth/BluetoothHidDevice.java)。 |
| ADB 转发与 USB 即插即用 | 本机 `adb help` 确认 `adb -s SERIAL forward tcp:29181 localabstract:touchpad_usb` 的语法；原计划另一处的 `tcp:29180;tcp:29180` 与本地抽象套接字设计冲突。ADB 需要设备开启调试并授权，USB 网络共享需要用户开启，均不是默认的无操作即插即用。端口、设备断连和多设备选择应进入验收。 |
| 全程 <2 ms、ADB 往返 0.25–0.45 ms、240 Hz、DSCP 必进 WMM 语音队列、JNI/IPC <50 μs | 当前仓库没有实测数据；这些值与网络设备、屏幕采样率、系统调度和主机注入有关。删除硬性承诺，先建立端到端测量；DSCP 仅作为可配置实验，需确认操作系统和 AP 实际如何处理。 |
| macOS 私有 CGS 手势与“真触控板”效果 | 计划中的私有符号、字段常量、Mission Control/Spaces 连续手势以及橡皮筋效果没有可复现设备验证。首版仅以公开的 CoreGraphics 鼠标、滚轮事件做样机；系统动作可验证用户配置的快捷键或辅助功能方案，私有 API 不作为交付前提。 |

## 2. 产品范围与交互

首版目标是将 Android 手机变成一块可连接 macOS/Windows 主机的触控区域：单指移动与轻触左键、双指移动滚动与轻触右键、触摸按下时取消惯性、右侧/底部区域单指滚动、连接状态与灵敏度设置。滚动方向、区域宽度、振动强度可配置。OLED 深色页面与自动隐藏的状态控件列入首版；黑色页面与亮度调整只作为省电尝试，防烧屏效果不作保证。

首版连接按 **局域网优先、ADB 有线作为第二条路径** 落地。蓝牙 HID 先以单独样机验证 Android 设备与 macOS/Windows 的普通鼠标兼容性，再决定是否纳入首版。USB 网络共享、Linux `/dev/uinput`、iOS、原生 3/4 指手势、驱动级虚拟触控板列为后续能力。

手势状态机必须定义 Down/Move/Up/Cancel、丢包/断连时按键释放、模式切换时惯性终止、双指与边缘滚动的优先级，以及底部和右侧重叠角的归属。双击按住拖动、三指中键保留为后续需求，不在首版状态机中提前写死未经实测的毫秒阈值。

## 3. 建议架构

```text
Android：NSGo 设置页 + 原生采样视图（实验验证后接入）
        ├─ 局域网：发现 / 配对 / 加密事件通道
        ├─ ADB：授权后的本机端口转发
        └─ 可选蓝牙 HID：独立的鼠标报告通道
                  ↓
Go：事件编解码 → 状态机 → 滤波 / 加速度 / 滚动映射
                  ↓
桌面：macOS CoreGraphics / Windows SendInput → 普通鼠标与滚轮
```

先做可运行的直连样机，再决定是否需要绕开 NSGo 事件通道。Android 原生 `View` 应记录 `MotionEvent` 当前样本及历史样本、时间戳和指针 ID；用真实设备对比默认触摸路径与原生桥接的频率和延迟。如果确需高频路径，固定 NSGo fork 的提交与构建脚本，使用本地套接字传输二进制样本；Java 与 Go 的坐标系、取消事件、进程重启都要在样机中验证。`HIGH_SAMPLING_RATE_SENSORS` 不作为触摸屏采样率保证。

网络协议先确定事件类型、版本、序列号、会话 ID、时间戳、坐标单位和最大包长；不能仅凭“24–64 字节”定死格式。移动事件允许丢旧包；按键松开、滚动结束、会话重置必须有确认、重试和超时释放机制，单纯重复发送两次不能保证可靠。配对时使用经过审查的密钥交换或有明确威胁模型的安全通道；二维码可承载一次性配对信息，后续会话需要认证与加密，并处理重放和重连。发现服务只负责定位，不能成为身份凭据。LAN 与 ADB 复用同一状态机，切换链路时先释放主机按键状态。

滤波器、加速度曲线、惯性衰减和振动间隔采用可配置参数，用录制的触摸轨迹调参；原计划的 1-Euro 与 sigmoid 可作候选实现，但常数不能当成设备通用最优值。主机滚轮可能已有系统加速，需评估是否出现重复加速。

## 4. 阶段、交付物与验收

| 阶段 | 交付物 | 验收方式 |
| --- | --- | --- |
| 0. 工具链与实测基线 | 固定 NSGo 提交；最小 Android + macOS + Windows 工程；设备与 OS 版本清单；采样/时延测量脚本 | 实际编译目标产物；Android 真机安装，macOS 和 Windows 各启动一次；记录采样间隔分布与主机收到事件的分布。 |
| 1. 最小输入闭环 | Android 单指移动/点击，经局域网发送到 macOS 和 Windows；断线释放按键 | 连续移动与点击、切后台、断网、重连均不遗留按键按下；两端都可复现。 |
| 2. 协议与配对 | 版本化消息、序列验证、认证加密、二维码配对、局域网发现 | 未配对设备无法注入事件；重放、乱序、丢包、重复按键转换与重新配对有可复现测试。 |
| 3. 交互与显示 | 双指滚动/右键、边缘单指滚动、惯性取消、触感与黑色界面 | 在浏览器、文件管理器、文本编辑器中分别检查滚动方向与连续性；手势转换不误触；亮度/振动在不支持的设备上有退化行为。 |
| 4. 有线与性能 | ADB 转发、设备选择与重连；端到端测量报告 | 测试 USB 调试未授权、多个设备、拔插、端口占用；报告端到端 p50/p95/p99 与丢包率，分别标出采样、传输、注入耗时和测量方法，再决定是否设置延迟指标。 |
| 5. 蓝牙可行性门槛 | 独立 Android HID 鼠标样机与主机兼容记录 | 两种桌面系统分别实测配对、移动、点击、滚轮、前后台切换与断连；通过后才将蓝牙模式写入发布范围。 |

## 5. 待验证事项与后续路线

- macOS 连续滚动的相位字段、辅助功能授权和不同应用的反馈：先做公开 API 样机再定实现；私有 CGS 与系统级 3/4 指滑动不列入首版承诺。
- Windows 的触摸注入只在目标应用确有需求时试验；精确触控板需要单独研究 HID 描述符、设备连接方式、签名/驱动部署与兼容测试。
- USB 网络共享的网段、接口名称和系统支持因设备而异，不能硬编码 `192.168.42.0/24` 等地址作为检测条件。
- 蓝牙 HID 的连接间隔、QoS、主机手势、报文描述符支持和前后台限制必须分别在实际设备验证；不能承诺由 QoS 参数决定固定低延迟。
- OLED 位移 HUD、亮度下限和隐藏系统手势边缘都受设备与系统限制；保证有可访问的退出、返回和设置入口。
- 后续需求：双击按住拖动、三指中键、Linux 与 iOS、USB 网络共享、真正的虚拟触控板驱动。每项先有独立样机与验收证据再排期。

## 6. 资料边界

查证来源：上表链接均指向本次实际查看的上游源码或 Microsoft 文档；本机 `adb help` 为命令语法证据。Android 官方网站在本次环境中访问超时，因此蓝牙行为引用的是已取回的 AOSP Android 9 源码，不能外推为所有 Android 新版本及设备的实测结论。仓库中的 `docs/kb-nsgo-android-macos.md`、`docs/kb-virtual-hid-and-input.md` 和 `docs/kb-transport-and-algorithms.md` 仍有与本计划冲突的未经验证描述，实施时以本计划与新完成的样机测量为准。
