# Seedance 视频生成接口调用说明

更新日期：2026-11-20。文中示例均在线上实际运行通过。

一次调用就能生成视频：把提示词和参考素材发给网关，网关负责存储附件、把人像登记到厂商素材库、排队，任务结束后自动清理。本文说明怎样提交任务、查询进度和下载视频。

## 快速开始

生成一个视频分三步：提交任务拿到任务号，查询任务状态，状态变成 `completed` 后下载视频。下面的命令可以直接复制，先把地址和密钥设成环境变量。

```bash
export BASE=https://newapi.vyxsy.com
export KEY=sk-你的密钥
```

### 第一步：提交任务

```bash
curl -s "$BASE/v1/videos" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "doubao-seedance-2-5-flash-261115",
    "prompt": "一只橘猫在阳光下的草地上奔跑，镜头跟随，画面写实",
    "seconds": 5,
    "metadata": {"resolution": "480p", "ratio": "16:9"}
  }'
```

返回里的 `id` 就是任务号：

```json
{"id":"task_2n4uR2JDNiZBWfGKEuzuSPN6qtxkvH3y","object":"video","model":"doubao-seedance-2-5-flash-261115","status":"queued","progress":0,"created_at":1795163021}
```

### 第二步：查询任务

每 10 秒查一次，直到 `status` 变成 `completed` 或 `failed`。

```bash
curl -s "$BASE/v1/videos/task_2n4uR2JDNiZBWfGKEuzuSPN6qtxkvH3y" \
  -H "Authorization: Bearer $KEY"
```

```json
{"id":"task_2n4uR2JDNiZBWfGKEuzuSPN6qtxkvH3y","object":"video","model":"doubao-seedance-2-5-flash-261115","status":"completed","progress":100,"created_at":1795163021,"completed_at":1795163131}
```

### 第三步：下载视频

```bash
curl -s -o video.mp4 "$BASE/v1/videos/task_2n4uR2JDNiZBWfGKEuzuSPN6qtxkvH3y/content" \
  -H "Authorization: Bearer $KEY"
```

得到的 `video.mp4` 就是成品。这个例子是 480p、5 秒的文生视频，实测 85 秒生成完，用量 48,437 tokens，费用约 ¥2.2。

## 地址、密钥和可用模型

外网只能访问 `/v1/` 开头的路径，本文的接口都在这个范围内；最后一节的原生接口只能在内网用。

| 从哪里调用 | 地址 |
| --- | --- |
| 外网 | `https://newapi.vyxsy.com` |
| 公司内网 | `http://192.168.19.111:49632` |

每个请求都要带密钥：`Authorization: Bearer sk-你的密钥`。密钥在控制台创建。

| 模型 | 线路 | 分辨率 | 时长 |
| --- | --- | --- | --- |
| `doubao-seedance-2-5-260628` | 大陆（火山方舟） | 480p、720p、1080p、4k | 4–30 秒 |
| `doubao-seedance-2-5-flash-261115` | 大陆（火山方舟） | 480p、720p | 4–30 秒 |
| `dreamina-seedance-2-5-260628` | 海外（BytePlus） | 480p、720p、1080p、4k | 4–30 秒 |
| `dreamina-seedance-2-5-flash-261115` | 海外（BytePlus） | 480p、720p | 4–30 秒 |

带 `flash` 的模型更快、更便宜：同样 480p、5 秒，实测 flash 约 1–1.5 分钟完成，标准版约 2.5–3.5 分钟。

提交时返回 `model_not_found`，说明这个密钥还没有开通视频渠道，请联系管理员。

## 提交任务：POST /v1/videos

请求体是 JSON；要直接上传文件时改用表单，见下一节。提交成功立即返回任务号，视频在后台生成。

| 参数 | 是否必填 | 说明 |
| --- | --- | --- |
| `model` | 必填 | 模型名，见上表 |
| `prompt` | 必填 | 提示词。有多张参考图时，可以用“图1”“图2”按顺序指代 |
| `seconds` | 请务必填 | 视频时长，4–30 的整数 |
| `metadata.resolution` | 请务必填 | 分辨率：`480p`、`720p`、`1080p`、`4k` |
| `metadata.ratio` | 可选 | 画面比例：`16:9`、`4:3`、`1:1`、`3:4`、`9:16`、`21:9`、`adaptive`。用图片作首帧时不要填 |
| `metadata.content` | 可选 | 参考素材列表，见下一节 |
| `metadata.portrait` | 可选 | 填 `true` 表示所有参考图片和视频里都有人物，见“图片里有人物”一节 |
| `metadata.generate_audio` | 可选 | 是否同时生成声音，默认 `true` |
| `metadata.watermark` | 可选 | 是否加水印，`true` 或 `false` |
| `metadata.seed` | 可选 | 随机种子，整数 |

`seconds` 和 `metadata.resolution` 不填也能提交，但网关会按 30 秒、1080p 预扣费，标准版一次约 ¥112，余额不够会直接返回 403。所以请每次都填。

提交成功的返回：

```json
{"id":"task_DjwZC87PF8zkPgmo217SkV4tbZsb28ek","object":"video","model":"doubao-seedance-2-5-260628","status":"queued","progress":0,"created_at":1795163026}
```

保存好 `id`，查询和下载都要用它。

## 参考素材：图片、视频和音频

参考素材放在 `metadata.content` 里，每一项写明类型、地址和角色：

```json
{"type": "image_url", "image_url": {"url": "https://example.com/a.jpg"}, "role": "reference_image"}
```

| 素材 | `type` | 地址写在 | 可用的 `role` |
| --- | --- | --- | --- |
| 图片 | `image_url` | `image_url.url` | `reference_image` 参考图、`first_frame` 首帧、`last_frame` 尾帧 |
| 视频 | `video_url` | `video_url.url` | `reference_video` |
| 音频 | `audio_url` | `audio_url.url` | `reference_audio` |

角色的规则是厂商的要求，已逐条验证：

- 有多张图片时，每张都要写角色。
- 只有一张图片且不写角色，等同于首帧。
- 首帧、尾帧不能和参考素材混用；尾帧必须和首帧一起出现。
- 用了首帧或尾帧时不要填 `metadata.ratio`，画面比例跟随首帧图片。

素材的地址有三种给法，可以在同一个请求里混用前两种。

### 方式一：公网链接

素材已经有公网地址时用这种，最省事。下例中第一张图有人物，所以加了 `"portrait": true`。

```bash
curl -s "$BASE/v1/videos" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "doubao-seedance-2-5-260628",
    "prompt": "图1中的人物穿着图2的服装，在室内自然地向前走两步并转身，镜头缓慢推近",
    "seconds": 5,
    "metadata": {
      "resolution": "480p",
      "ratio": "3:4",
      "watermark": false,
      "content": [
        {"type": "image_url", "image_url": {"url": "https://example.com/person.jpg"}, "role": "reference_image", "portrait": true},
        {"type": "image_url", "image_url": {"url": "https://example.com/outfit.jpg"}, "role": "reference_image"}
      ]
    }
  }'
```

实测：提交用时 4.6 秒，生成用时约 3 分钟。

### 方式二：把文件放进请求（base64）

素材没有公网地址时，把文件编码成 `data:` 链接填在 `url` 里，格式是 `data:<文件类型>;base64,<内容>`。网关会替你把文件存到对象存储，再把链接交给厂商。

```python
import base64
import os
import requests

BASE = os.environ["BASE"]
KEY = os.environ["KEY"]


def data_url(path, mime="image/jpeg"):
    with open(path, "rb") as f:
        return "data:%s;base64,%s" % (mime, base64.b64encode(f.read()).decode())


body = {
    "model": "doubao-seedance-2-5-flash-261115",
    "prompt": "图1中的人物在图2的场景里自然地向前走两步并转身，镜头缓慢推近",
    "seconds": 5,
    "metadata": {
        "resolution": "480p",
        "ratio": "3:4",
        "content": [
            {"type": "image_url", "image_url": {"url": data_url("person.jpg")},
             "role": "reference_image", "portrait": True},
            {"type": "image_url", "image_url": {"url": data_url("scene.jpg")},
             "role": "reference_image"},
        ],
    },
}
resp = requests.post(BASE + "/v1/videos", json=body, timeout=900,
                     headers={"Authorization": "Bearer " + KEY})
print(resp.status_code, resp.text)
```

实测：两张图共 2.8 MB，提交用时 9.3 秒，生成用时约 1 分钟。

### 方式三：表单上传文件

用 `multipart/form-data` 直接上传文件，其余参数作为表单字段，`metadata` 填一段 JSON 文本。

```bash
curl -s "$BASE/v1/videos" \
  -H "Authorization: Bearer $KEY" \
  -F model=doubao-seedance-2-5-260628 \
  -F prompt='图1中的人物穿着图2的服装，在室内自然地向前走两步并转身，镜头缓慢推近' \
  -F seconds=5 \
  -F 'metadata={"resolution":"480p","ratio":"3:4","portrait":true}' \
  -F images=@person.jpg \
  -F images=@outfit.jpg
```

实测：提交用时 4.3 秒，生成用时约 3.5 分钟。

| 表单字段 | 说明 |
| --- | --- |
| `model`、`prompt`、`seconds` | 与 JSON 中的同名参数相同 |
| `metadata` | 一段 JSON 文本，内容与 JSON 请求里的 `metadata` 相同 |
| `images` | 图片文件，可以重复多次；也接受 `image`、`input_reference` |
| `videos` | 参考视频文件；也接受 `video` |
| `audios` | 参考音频文件；也接受 `audio` |
| `image_role` | 图片的角色。不填时：只传一张图当首帧，传多张图都当参考图 |

表单上传有两点要注意：

- 无法单独标记某一张图有人物，只能用 `metadata` 里的 `"portrait":true` 整体标记。要逐张标记请用方式一或方式二。
- 只传一张图又想把它当参考图而不是首帧，加上 `-F image_role=reference_image`。

## 图片里有人物：portrait 标记

带人物的图片和视频必须加 `portrait` 标记，否则厂商会拒绝。Seedance 只接受已经登记在厂商素材库里的人像，登记由网关代办，你只需要标出哪些素材有人。

| 要标记的范围 | 写法 |
| --- | --- |
| 某一个素材 | 在 `content` 的那一项里加 `"portrait": true` |
| 整个请求的所有图片和视频 | 在 `metadata` 里加 `"portrait": true` |
| 整体标记后排除某一项 | 在那一项里加 `"portrait": false` |

有人物却没标记时，提交会返回 400，错误里的 `content[0]` 指第 1 个素材：

```json
{"code":"fail_to_fetch_task","message":"{\"error\":{\"code\":\"InputImageSensitiveContentDetected.PrivacyInformation\",\"message\":\"The request failed because the input image 'content[0]' may contain real person. ...\",\"param\":\"content[0]\",\"type\":\"BadRequest\"}}","data":null}
```

给它加上标记后重发即可，这次失败不扣费。

使用时注意：

- 只标记真正有人物的素材。每标记一个，网关就要多做一次登记，提交会多等几秒；登记有频率限制，标得越多等得越久。
- `true` 只用于 AI 生成的虚拟人物。厂商不允许把真人照片登记成虚拟人像。
- 真人照片需要本人先完成真人认证，这项功能暂未开放，有需要请联系管理员。
- 登记的素材在任务结束后由网关自动删除，不需要你清理。

## 查询任务和下载视频

查询用 `GET /v1/videos/{任务号}`，建议每 10 秒查一次。

| `status` | 含义 |
| --- | --- |
| `queued` | 已接收，等待开始 |
| `in_progress` | 生成中 |
| `completed` | 生成完成，可以下载 |
| `failed` | 生成失败，`error` 里有原因，预扣的费用会退回 |

生成完成时：

```json
{"id":"task_DjwZC87PF8zkPgmo217SkV4tbZsb28ek","object":"video","model":"doubao-seedance-2-5-260628","status":"completed","progress":100,"created_at":1795163026,"completed_at":1795163221}
```

生成失败时：

```json
{"id":"task_...","object":"video","model":"doubao-seedance-2-5-260628","status":"failed","progress":100,"created_at":1795078873,"error":{"code":"厂商的错误码","message":"厂商给出的原因"}}
```

实测的生成时间：

| 模型与规格 | 从提交到完成 |
| --- | --- |
| flash，480p，5 秒 | 1–1.5 分钟 |
| 标准版，480p，5 秒 | 2.5–3.5 分钟 |
| 标准版，1080p，15 秒 | 3.5–4.5 分钟 |

下载用 `GET /v1/videos/{任务号}/content`，返回的就是 MP4 文件本身（`Content-Type: video/mp4`），同样要带密钥。

- 请在生成后 24 小时内下载并自行保存，厂商只将成品保留 24 小时。
- 下载接口只支持 GET，用 HEAD 请求会返回 502。
- 成品带声音；不需要声音时在提交时加 `"generate_audio": false`。

## 等待、超时和重试

提交这一次调用的 HTTP 超时请设到 15 分钟。带人物素材时，网关要在这次调用里完成存储、登记和排队，然后才返回任务号。

| 情况 | 提交这一步的实测耗时 |
| --- | --- |
| 纯文字，或素材都不含人物 | 5–6 秒 |
| 1–3 个带人物的素材 | 4–20 秒 |
| 带人物的素材超过 3 个 | 1 分钟以上，因为每个账号每分钟只能登记 3 个 |
| 素材库已满，或很多人同时提交 | 排队等待，最长 10 分钟，超时返回 429 |

下面这些错误表示“现在太忙”，原样重发即可：

| HTTP | `code` | 含义 | 处理 |
| --- | --- | --- | --- |
| 429 | `asset_library_full` | 素材库满了，排队超时 | 等 30 秒以上再发 |
| 429 | `asset_upload_rate_limited` | 登记频率超限，排队超时 | 等 1 分钟再发 |
| 409 | `asset_processing` | 素材库还在处理你的文件 | 等 30 秒再发，已登记的文件不会重复登记 |
| 502 | `asset_library_failed`、`media_storage_failed` | 网关访问厂商或存储失败 | 稍后再发 |

其余的 4xx 错误是请求本身的问题，重发没有用，请按“错误码”一节修改。

两点提醒：

- 上一次提交还没返回时，不要重复提交同一个请求。两次都会成功，会生成两个视频并扣两次费。
- 客户端先超时断开的话，这次提交是否成功无法确认，所以超时一定要设够长。

## 费用

按用量（tokens）计费：视频越长、分辨率越高，用量越多。提交时按估算预扣，生成完成后按实际用量多退少补；提交被拒绝或生成失败不扣费。

估算公式：用量 ≈ 时长（秒）× 宽 × 高 × 24 ÷ 1024。带参考视频时，参考视频的时长也计入用量，但单价更低。

当前单价，每百万 tokens（以控制台价格页为准）：

| 模型 | 480p、720p | 1080p | 4k | 带参考视频时 |
| --- | --- | --- | --- | --- |
| `doubao-seedance-2-5-260628` | ¥70 | ¥77 | ¥31 | ¥42、¥46、¥19 |
| `doubao-seedance-2-5-flash-261115` | ¥46 | 不支持 | 不支持 | ¥28 |
| `dreamina-seedance-2-5-260628` | $10.7 | $11.7 | $4.7 | $6.4、$7.0、$2.9 |
| `dreamina-seedance-2-5-flash-261115` | $7.0 | 不支持 | 不支持 | $4.3 |

海外模型按美元计价，按 1 美元 = 7 元折算。“带参考视频时”一列的三个价格依次对应前三列的分辨率。

实际账单举例：

| 规格 | 用量 | 费用 |
| --- | --- | --- |
| flash，480p，16:9，5 秒，文生视频 | 48,437 tokens | ¥2.23 |
| 标准版，480p，3:4，5 秒，两张参考图 | 49,761 tokens | ¥3.48 |
| flash，480p，16:9，4 秒，带一段 5 秒的参考视频 | 86,867 tokens | ¥2.43 |
| 标准版，1080p，3:4，15 秒，五张参考图 | 732,108 tokens | ¥56.37 |
| 海外标准版，1080p，3:4，15 秒，五张参考图 | 732,108 tokens | ¥59.96 |

两点提醒：

- 余额低于预扣金额时，提交会返回 403，错误码 `pre_consume_token_quota_failed`，消息里写明了当前余额和需要的金额。
- 参考图片和人像登记本身不另外收费。

## 错误码

错误返回有两种格式，解析时两种都要兼容：错误码可能在最外层的 `code`，也可能在 `error.code`。

```json
{"code":"invalid_portrait","message":"content[0]: portrait must be true, false or the asset group of a verified person","data":null}
```

```json
{"error":{"code":"model_not_found","message":"None of the channels configured on this key can serve model ...","type":"new_api_error"}}
```

| HTTP | `code` | 含义 | 处理 |
| --- | --- | --- | --- |
| 400 | `fail_to_fetch_task` | 厂商拒绝了请求，`message` 里是厂商的原始错误 | 对照下一张表修改 |
| 400 | `plugin_request_invalid` | 参数不被该模型支持，例如给 flash 模型传 `1080p` | 换成支持的值 |
| 400 | `invalid_portrait` | `portrait` 的值不对 | 改成 `true` 或 `false` |
| 400 | `invalid_inline_media` | 内嵌文件的类型不支持、不是 base64、为空或超过 64 MB | 按“限制”一节检查文件 |
| 400 | `asset_rejected` | 厂商素材库拒绝了某个带人物的素材，`message` 里有原因 | 换素材 |
| 400 | `asset_request_too_large` | 一个请求里带人物的素材超过 50 个 | 减少标记的素材 |
| 400 | `portrait_disabled`、`asset_library_unavailable` | 这条线路没有开通人像登记 | 联系管理员 |
| 401 | 空 | 密钥无效，消息为 `Invalid token` | 检查密钥 |
| 403 | `pre_consume_token_quota_failed` | 余额不足以预扣 | 充值，或降低分辨率和时长 |
| 409 | `asset_processing` | 素材库还在处理文件 | 稍后原样重发 |
| 429 | `asset_library_full`、`asset_upload_rate_limited` | 排队超时 | 稍后原样重发 |
| 502 | `asset_library_failed`、`media_storage_failed` | 网关访问厂商或存储失败 | 稍后原样重发 |
| 503 | `model_not_found` | 密钥没有可用的视频渠道 | 联系管理员 |

常见的厂商错误，出现在 `fail_to_fetch_task` 的 `message` 里，或失败任务的 `error` 里：

| 厂商错误 | 含义 | 处理 |
| --- | --- | --- |
| `InputImageSensitiveContentDetected.PrivacyInformation` | 素材里有人物但没加 `portrait` 标记 | 给提示的那一项加标记 |
| `InvalidParameter`，`param` 为 `duration` 或 `ratio` | 时长或比例的取值不对 | 时长 4–30，比例见“限制”一节 |
| `InvalidParameter.TaskTypeConstraint` | 参数与任务类型冲突 | 首帧模式去掉 `ratio`；视频编辑把 `ratio` 改为 `adaptive`、`seconds` 改为 `-1` |
| `InvalidParameter`，消息含 `resource not found` | 厂商取不到这个素材 | 确认链接能从公网打开 |
| `InvalidParameter`，消息含 `role` | 角色没写、写错或混用 | 见“参考素材”一节的规则 |

## 限制

| 项目 | 限制 |
| --- | --- |
| 时长 | 4–30 秒的整数 |
| 画面比例 | `16:9`、`4:3`、`1:1`、`3:4`、`9:16`、`21:9`、`adaptive` |
| 分辨率 | 标准版 `480p`、`720p`、`1080p`、`4k`；flash 只有 `480p`、`720p` |
| 图片文件 | JPEG、PNG、WebP、GIF、BMP、TIFF、HEIC、HEIF |
| 视频文件 | MP4、MOV、WebM、MKV、AVI |
| 音频文件 | MP3、WAV、M4A、AAC、OGG、FLAC |
| 单个文件 | 不超过 64 MB |
| 整个请求 | 不超过 128 MB；用 base64 时文件会变大约三分之一 |
| 人像登记 | 每个账号每分钟 3 个，同时最多保留 50 个 |

文件类型和大小是网关接受的范围。厂商对素材的尺寸、时长和数量另有要求，超出时会在提交时报错。

两条特殊规则：

- 用参考视频做编辑（例如替换画面里的主体）时，`metadata.ratio` 必须填 `adaptive`，`seconds` 必须填 `-1`，成品的比例和时长跟随参考视频。`-1` 会按 30 秒预扣费，请保证余额充足。
- 只参考视频的运镜、动作时按普通规则填写。实测：上传一段 5 秒的参考视频，生成 4 秒成品，39 秒完成。

以下功能只做过参数校验，还没有实际生成过：音频参考、首帧和尾帧、4k 分辨率。

## 完整示例：从本地图片到成品视频

下面的 Python 脚本上传本地图片、提交任务、等待完成并保存视频，遇到“太忙”的错误会自动重试。需要先安装 `requests`。

```python
"""Seedance 视频生成完整示例：上传本地图片 -> 提交 -> 轮询 -> 下载。

用法：
    export BASE=https://newapi.vyxsy.com
    export KEY=sk-你的密钥
    python3 seedance_demo.py person.jpg outfit.jpg
"""
import json
import os
import sys
import time

import requests

BASE = os.environ["BASE"].rstrip("/")
HEADERS = {"Authorization": "Bearer " + os.environ["KEY"]}
# 这些错误码表示“现在太忙”，过一会儿原样重发即可。
RETRY_LATER = {"asset_library_full", "asset_upload_rate_limited", "asset_processing"}


def submit(paths):
    metadata = {"resolution": "480p", "ratio": "3:4", "portrait": True}
    fields = {
        "model": "doubao-seedance-2-5-260628",
        "prompt": "图1中的人物穿着图2的服装，在室内自然地向前走两步并转身，镜头缓慢推近",
        "seconds": "5",
        "metadata": json.dumps(metadata),
        # 只传一张图时，默认把它当作视频的第一帧；要当参考图就加上这一项。
        "image_role": "reference_image",
    }
    for attempt in range(5):
        files = [("images", (os.path.basename(p), open(p, "rb"), "image/jpeg")) for p in paths]
        try:
            # 素材入库和排队都发生在这次调用里，超时要设长。
            resp = requests.post(BASE + "/v1/videos", headers=HEADERS, data=fields, files=files, timeout=900)
        finally:
            for _, (_, handle, _) in files:
                handle.close()
        if resp.status_code == 200:
            return resp.json()["id"]
        try:
            error = resp.json()
        except ValueError:
            error = {}
        code = error.get("code") or (error.get("error") or {}).get("code")
        if code in RETRY_LATER:
            print("网关繁忙（%s），30 秒后重试" % code)
            time.sleep(30)
            continue
        raise SystemExit("提交失败：HTTP %s %s" % (resp.status_code, resp.text))
    raise SystemExit("多次重试后仍然繁忙，请稍后再试")


def wait(task_id):
    while True:
        task = requests.get(BASE + "/v1/videos/" + task_id, headers=HEADERS, timeout=60).json()
        print(time.strftime("%H:%M:%S"), task.get("status"), task.get("progress"))
        if task.get("status") == "completed":
            return
        if task.get("status") == "failed":
            raise SystemExit("生成失败：%s" % json.dumps(task.get("error"), ensure_ascii=False))
        time.sleep(10)


def download(task_id, path):
    with requests.get(BASE + "/v1/videos/" + task_id + "/content", headers=HEADERS, stream=True, timeout=600) as resp:
        resp.raise_for_status()
        with open(path, "wb") as out:
            for chunk in resp.iter_content(1 << 20):
                out.write(chunk)
    print("已保存", path, os.path.getsize(path), "字节")


if __name__ == "__main__":
    task_id = submit(sys.argv[1:])
    print("任务号", task_id)
    wait(task_id)
    download(task_id, task_id + ".mp4")
```

实际运行的输出（中间省略了重复的进度行）：

```text
任务号 task_n2jQ9JHXNpS8Ul0xT6TFJdG0YFmTY189
16:25:01 queued 0
16:25:11 in_progress 50
...
16:27:31 in_progress 50
16:27:41 completed 100
已保存 task_n2jQ9JHXNpS8Ul0xT6TFJdG0YFmTY189.mp4 2733372 字节
```

## 内网原生接口（火山方舟格式）

已经按火山方舟格式写好代码的调用方，可以在内网直接用原生接口：请求体与方舟一致，只多了 `portrait` 标记。这两个接口不对外网开放。

| 操作 | 接口 |
| --- | --- |
| 提交 | `POST /doubao/api/v3/contents/generations/tasks` |
| 查询 | `GET /doubao/api/v3/contents/generations/tasks/{任务号}` |

```bash
curl -s "http://192.168.19.111:49632/doubao/api/v3/contents/generations/tasks" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "doubao-seedance-2-5-260628",
    "content": [
      {"type": "text", "text": "图1中的人物在图2的场景里自然地向前走两步并转身"},
      {"type": "image_url", "image_url": {"url": "https://example.com/person.jpg"}, "role": "reference_image", "portrait": true},
      {"type": "image_url", "image_url": {"url": "https://example.com/scene.jpg"}, "role": "reference_image"}
    ],
    "resolution": "480p",
    "ratio": "3:4",
    "duration": 5,
    "watermark": false
  }'
```

提交返回 `{"id":"task_..."}`。查询返回方舟格式的任务，`status` 取值为 `queued`、`running`、`succeeded`、`failed`：

```json
{"id":"task_dyKv7ygX0wSsYQYrJwpGTgPSbMENWw0x","model":"doubao-seedance-2-5-260628","status":"succeeded","content":{"video_url":"https://ark-acg-cn-beijing.tos-cn-beijing.volces.com/...mp4?..."},"resolution":"480p","ratio":"3:4","duration":5,"framespersecond":24,"generate_audio":true,"usage":{"completion_tokens":49761,"total_tokens":49761},"created_at":1795155525,"updated_at":1795155679}
```

与 `/v1/videos` 的参数对照：

| `/v1/videos` | 原生接口 |
| --- | --- |
| `prompt` | `content` 里 `type` 为 `text` 的一项 |
| `seconds` | `duration` |
| `metadata.resolution`、`metadata.ratio` 等 | 最外层的 `resolution`、`ratio` 等 |
| `metadata.content` | `content` |
| `metadata.portrait` | 最外层的 `portrait` |

使用时注意：

- 素材同样可以用 base64 的 `data:` 链接；原生接口不支持表单上传。
- `content.video_url` 是厂商给的下载链接，24 小时内有效。海外模型的链接在国内下载很慢，建议改用 `GET /v1/videos/{任务号}/content`。
- 原生接口提交的任务，也能用 `/v1/videos/{任务号}` 查询和下载。
