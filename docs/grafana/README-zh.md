# Grafana 仪表盘

`rospanel.json` 是面板自身指标的现成仪表盘：用户、流量与吞吐，集群中每台服务器一行，以及面板
宿主本身。在 Grafana 中通过 **仪表盘 → 新建 → 导入** 导入，上传该文件，并在提示时选择你的
Prometheus 数据源。

## 让 Prometheus 指向面板

指标端点是 `GET https://<panel>/<api-path>/v1/metrics`。它位于与外部 API 其余部分相同的 API
密钥之后，因此抓取时必须携带一把密钥。在 **设置 → API** 中创建密钥，然后：

```yaml
scrape_configs:
  - job_name: rospanel
    scheme: https
    metrics_path: /<api-path>/v1/metrics
    authorization:
      credentials: rp_your_api_key_here
    static_configs:
      - targets: ['panel.example.com']
```

**密钥不是只读的。** 面板没有只读的 API 密钥：任何有效密钥都会打开整个 `/v1` 接口，包括创建
和删除用户与服务器。`prometheus.yml` 这类文件往往对所有用户可读，而且会被提交到配置仓库，因此
请把它里面的密钥当作面板凭据来对待——限制文件权限、不要纳入版本控制，并给这次抓取单独一把密钥，
以便单独撤销。

## 图表期待什么

一切都从同一个目标抓取，因此没有内置 `instance` 或 `job` 过滤。当某个图表看起来是空的而不是
显示错误时，有两点值得知道：

- **每台服务器的 CPU、内存和磁盘**只会由已经签到的节点 agent 上报。添加了但从未安装的服务器
  完全没有主机统计，因此会从这三个图表中消失，而不是显示为零。
- **已绑定设备**只在设备绑定（HWID）开启时才会导出。

流量图表在运营者时区的本地午夜重置，而不是滚动的 24 小时——这是面板本身统计的方式，因此仪表盘
跟随它，而不是另造一套定义。
