# 项目介绍

Jellyfin Remora is a companion supervisor for Jellyfin on Darwin, Linux and Windows.
It keeps Jellyfin attached to healthy storage, starts it only when its data paths are safe, and stops it when network or disk damage is detected.

# 总体设计逻辑

- GO语言
- 通常相关文件与Jellyfin的数据目录放在一起
- 所有配置写在一个单独的YAML，默认config.yml，通过jellyfin-remora -c config.yml启动
- 具备良好的Systemd、Windows计划任务、launchd兼容性

# 项目结构

守护服务会被拆分成两个程序，jellyfin-remora后台守护主程序和remoractl控制程序。
jellyfin-remora会暴露unix socket，和localhost的REST API监听端口，用来控制jellyfin-remora。

## 配置文件

该项目完整的yaml配置模板如下：

```yaml
restapi:
  listen: 127.0.0.1
  port: 8095

remora:
  server-start-timeout: 30 # Unit: seconds
  server-stop-timeout: 300

  heartbeat-interval: 1 # Unit: seconds
  health-api-hearbeat: 10 # uses API endpoint /health
  user-login-watchdog: # uses a real user to simulate login and access home page
    enabled: True
    heartbeat: 60
    user: remora # will be auto generated when user not found
    password: password

  data-dir: default # 'default' means the same as jellyfin.datadir, will save jellyfin.pid, jellyfin.state, .remora_api_key and other files
  logs:
    path: log # related to remora.data-dir
    level: info # debug, info, warning, error
    rotation-time: 24h # support h(ours), d(ays), w(eeks)
    rotation-size-mb: 30
    preserve-time: 7d

disk:
  - type: physical
    device: /dev/disk5s1
    uuid: 6136C7AC-F6D4-4648-88A2-9097A7AFD38E # device和uuid二选一
    target: /data
    permission: rw
    hearbeat: 2 # optional, means check this drive every remora.hearbeat-interval * heartbeat seconds. default 1 for physical disk, 3 for network disk
  - type: smb
    device: //192.168.1.20/NAS
    options: vers=3,noatime...
    user: admin
    password: abcd1234
    target: /media
    permission: r
  - type: nfs
    device: //192.168.1.20/config/jellyfin
    options: vers=3,noatime...
    user: admin # optional
    password: abcd1234 # optional
    target: /config
    permission: rw

jellyfin:
  path: /Applications/Jellyfin.app/Contents/MacOS # Automatically find jellyfin/Jellyfin/jellyfin.exe executable under this path
  data-dir: /Volumes/SSD/jellyfin/data
  config-dir: /Volumes/SSD/jellyfin/config
  cache-dir: /Volumes/SSD/jellyfin/cache
  log-dir: /Volumes/SSD/jellyfin/logs
  web-dir: default

  parameters: # items inside this element will be added into command parameter as --key=value, mostly should be empty
    hostwebclient: True
  # The following settings are inside server
  # default or null will leave section blank
  # all of the settings above are named the same as inside jellyfin web dashboard
  # remora will apply these settings by directly modifying XML files inside jellyfin.config-dir before the server start
  # however, remora will not flush these settings during init state(setup wizard). remora will manually stop server after init completed, then restart server like normal (including flushing settings)
  # the other settings not mentioned above this section are not supported, just change them inside dashboard after server start
  general:
    settings:
      server-name: NAS
    paths:
      cache-path: null
      metadata-path: default
    performance:
      parallel-library-scan-tasks-limit: 1
      parallel-image-encoding-limit: null
  branding:
    enable-splash-screen: true
    splash-screen-image: /Volumes/SSD/jellyfin/splash.png
    login-disclaimer: "Welcome to my Jellyfin!"
    custom-css-code: /Volumes/SSD/jellyfin/data/custom.css # Use text file path rather than raw css code
  playback:
    transcoding:
      transcode-path: /Volumes/SSD/jellyfin/cache/transcode
      enable-fallback-fonts: true
      fallback-font-folder-path: /Volumes/SSD/jellyfin/config/fonts
  networking:
    server-address-settings:
      local-http-port-number: 8096
      enable-https: false
      local-http-port-number: 8920
      base-url: null
      bind-to-local-network-address: null
    ip-protocols:
      enable-ipv4: true
      enable-ipv6: true

# Auto help setup wizard
init:
  server-name: Jellyfin
  display-language: English
  user: admin
  password: password
  preferred-metadata-language: Chinese
  preferred-metadata-region: United States
  allow-remote-connections: true
```

## jellyfin-remora 架构

此处列举jellyfin-remora的几个核心模块，相关外围的一些Helper和底层辅助模块自行设计。

- **Config:** 读取启动时指定的yml文件并给其它模块提供getter
- **APIClient:** 对请求Jellyfin的API抽象层
- **DiskHealthChecker:** 负责对配置文件给定的所有物理磁盘目录进行可读性/读写性校验，配置文件提供挂载目标时，也校验挂载健康（目前不支持SAN）
- **NFSHealthChecker:** 负责对CIFS/NFS网盘的健康状态进行校验，包括可读性/读写性、挂载健康、端口可达、权限检查
- **JellyfinHealthChecker:** 通过/health接口和内置账号凭据走API请求观测Jellyfin是否健康，通过Jellyfin数据目录下jellyfin.pid文件读取并观测进程是否存活、进程状态是否长期D或Z
- **HealthChecker:** 一个启动后默认每秒执行一次（称为Heartbeat，可调）的永久循环，分别根据频率设定并发调用上面的三个子HealthChecker，结果写入Jellyfin数据目录下jellyfin.state
- **StartStopHelper:** 一个启动后默认每秒执行一次（称为Heartbeat，可调）的永久循环，读取Jellyfin数据目录下jellyfin.state决定是否对Jellyfin服务进行启停、停止时是优雅停止还是kill -9强杀。启动状态和停止状态均存在超时。
- **InitServerHelper:** 协助通过API自动完成Jellyfin初始化流程、创建监管APIKEY。remora尽量控制涉及API访问jellyfin的行为使用自动申请的监管APIKEY，在没有监管APIKEY的时候（比如服务器首次启动）则会通过配置文件init.user定义的用户去登陆服务器创建一个APIKEY，并保存到隐藏文件.remora_api_key中
- **DiskUtil:** 代挂载硬盘和CIFS、NFS，权限不足时会发出Warning，但不会干涉启停决策。
- **RunningProcessManager:** 用于启动jellyfin-remora时Jellyfin服务器已经在运行的情况下对已存在的进程进行纳管。这个模块总是假设一个操作系统上可能存在多个Jellyfin实例，然后通过获取进程的路径和参数、获取获取进程监听的端口来与用户配置文件中填写的Jellyfin实例匹配，匹配成功则补充生成jellyfin.pid、jellyfin.state，让整个程序进入正常运行环节

## jellyfin-remora 设计

jellyfin-remora内置一个完整的状态机，涵盖jellyfin服务器以下状态：

未启动、启动中、运行中、停止中

可能还会有以下特殊状态：

首次启动、进程丢失、进程Hang，以及其它更多情况

启停服务器的核心依据有两个文件，jellyfin.pid和jellyfin.state，其中jellyfin.state每行一个数字，分别代表以下状态：

```
heartbeat_health: 服务器API是否正常响应，0代表正常，1代表不正常
disk_damage: 是否有磁盘损坏，0代表正常，1代表不正常，2代表不正常但不致命
manual_stop: 是否手动停止，0代表否，1代表是
```

所以，正常运行时的jellyfin.state应该是：

```
0
0
0
```

通过这两个文件的内容可以构建一个完整的决策树，支撑StartStopHelper模块决定是否对Jellyfin进程进行启停操作，jellyfin-remora主程序内部内存变量维护的状态机会比这个决策树复杂。

## remoractl

remoractl是一个命令行工具，在Unix系统默认通过/tmp的unix socket访问remora，在windows系统默认访问本地的8095端口。如果存在非标准化部署，可以通过--host=http://1.2.3.4:5678指定端口访问。

remoractl支持以下命令：

- start
- stop [--force]
- restart [--force]
- logs [-f --follow] [--since] [--until]
- status (展示jellyfin运行状态、uptime、端口、PID、路径、CPU用量、内存使用量、正在播放、转码任务数（伴生ffmpeg进程数）)
- healthcheck（立即触发一次所有的healthcheck并打印出结果）
- edit-config
- apikey [list] [create \<name\>] [delete \<name\>]
- session [list] [stop sessionid] （展示当前正在播放的会话，session-id前12位、用户、客户端、正在播放的媒体，并且通过stop命令尝试停止支持的客户端（部份第三方客户端可能会失败，cli返回Error: client xxx unsupported））

# 开发目标

当前是第一阶段，我为你提供了：
1. jellyfin 10.11的源码，并且已经codegraph index
2. jellyfin 12的完整API文档

这一阶段，需要完成Darwin平台（macOS）的适配开发，并预留windows和Linux接口
