<template>
  <div class="plyr-player-wrapper">
    <video ref="videoRef" class="plyr-video" playsinline></video>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, computed } from 'vue'
import Plyr from 'plyr'
import 'plyr/dist/plyr.css'
import Hls from 'hls.js'
import { useMessage } from 'naive-ui'
import { createDownloadUrl, type FileChild } from '@/api/file'

const props = defineProps<{
  file: FileChild
  autoplay?: boolean
  muted?: boolean
  preload?: 'auto' | 'metadata' | 'none'
}>()

const message = useMessage()

const videoRef = ref<HTMLVideoElement | null>(null)
const plyr = ref<Plyr | null>(null)
const hls = ref<Hls | null>(null)
const sourceUrl = ref('')

const autoplay = computed(() => !!props.autoplay)
const muted = computed(() => !!props.muted)
const preload = computed(() => props.preload || 'metadata')

const isHlsByName = (name: string) => name.toLowerCase().endsWith('.m3u8')
const isHlsByUrl = (url: string) => /\.m3u8(\?|$)/i.test(url)

// 判断该文件是否应走 HLS 播放。
//
// 优先信任后端下发的 isHls：
// 下发给前端的直链形如 /api/file/download/xxx?sign=...，从 URL 和后缀都看不出
// 格式，服务端 302 后才知道是不是 m3u8。早期版本用 fetch 去探测 302 最终地址，
// 但浏览器受同源策略限制（目标为云盘域名，跨域，redirect:'follow' 直接抛异常），
// 电脑端必然探测失败 → 误判为非 HLS → 原生播放器播 m3u8 → 黑屏。
// （手机 APP 的 WebView 不强制 CORS，探测能成功，所以只在电脑上复现。）
//
// 后端判定失败或旧版本后端未返回该字段时，退回后缀判断。
const shouldUseHls = (url: string, name: string, backendIsHls?: boolean): boolean => {
  if (isHlsByUrl(url) || isHlsByName(name)) return true

  return !!backendIsHls
}

// 初始化播放源并构建播放器
const initSource = () => {
  sourceUrl.value = ''

  createDownloadUrl({ fileId: props.file.id })
    .then((res) => {
      if (res.code === 200 && res.data?.downloadUrl) {
        sourceUrl.value = res.data.downloadUrl
        const name = props.file.name || ''
        // 以后端下发的 isHls 为准，不做任何网络探测（跨域会失败，导致电脑端误判）
        const useHls = shouldUseHls(sourceUrl.value, name, res.data.isHls)
        setupPlayer(useHls)
      } else {
        message.error(res.msg || '获取播放链接失败')
      }
    })
    .catch((e) => {
      console.error('createDownloadUrl error:', e)
      message.error('获取播放链接失败')
    })
}

const setupPlayer = (useHls = false) => {
  const video = videoRef.value
  if (!video || !sourceUrl.value) return

  // 清理已有实例
  destroyPlayer()

  // HLS 优先：hls.js（非 Safari），Safari 原生 HLS
  if (useHls && Hls.isSupported()) {
    hls.value = new Hls({
      // 可根据需要调整缓冲策略
      maxBufferLength: 30,
      liveDurationInfinity: true,
    })
    hls.value.loadSource(sourceUrl.value)
    hls.value.attachMedia(video)
    hls.value.on(Hls.Events.MANIFEST_PARSED, () => {
      buildPlyr()
      if (autoplay.value) {
        video.play().catch(() => {})
      }
    })
    hls.value.on(Hls.Events.ERROR, (_event, data) => {
      if (data?.fatal) {
        message.error('HLS 播放失败')
      }
    })
  } else {
    // 普通视频 / Safari 原生 HLS
    video.src = sourceUrl.value
    buildPlyr()
    if (autoplay.value) {
      video.play().catch(() => {})
    }

    // 兜底：原生播放失败时，尝试用 hls.js 重试（应对探测误判）
    const onNativeError = () => {
      if (Hls.isSupported() && !hls.value) {
        console.warn('原生播放失败，回退 hls.js 重试')
        hls.value = new Hls({ maxBufferLength: 30, liveDurationInfinity: true })
        hls.value.loadSource(sourceUrl.value)
        hls.value.attachMedia(video)
        hls.value.on(Hls.Events.MANIFEST_PARSED, () => {
          if (autoplay.value) video.play().catch(() => {})
        })
      }
    }
    video.addEventListener('error', onNativeError, { once: true })
  }

  // 初始设置
  video.preload = preload.value
  video.muted = muted.value
}

const buildPlyr = () => {
  const video = videoRef.value
  if (!video) return

  plyr.value = new Plyr(video, {
    controls: [
      'play-large',
      'play',
      'progress',
      'current-time',
      'mute',
      'volume',
      'settings',
      'fullscreen',
    ],
    muted: muted.value,
    autoplay: autoplay.value,
    loadSprite: true,
    invertTime: false,
    clickToPlay: true,
    disableContextMenu: true,
  })

  // 简单错误提示
  video.addEventListener('error', () => {
    message.error('视频播放出错')
  })
}

const destroyPlayer = () => {
  if (plyr.value) {
    plyr.value.destroy()
    plyr.value = null
  }
  if (hls.value) {
    hls.value.destroy()
    hls.value = null
  }
}

watch(
  () => props.file.id,
  () => {
    initSource()
  }
)

onMounted(() => {
  initSource()
})

onUnmounted(() => {
  destroyPlayer()
})
</script>

<style scoped>
.plyr-player-wrapper {
  position: relative;
  width: 100%;
  background: #000;
  border-radius: 8px;
  overflow: hidden;
}

.plyr-video {
  width: 100%;
  aspect-ratio: 16 / 9;
  height: auto;
  display: block;
}

/* 全屏时扩展高度 */
:fullscreen .plyr-video {
  height: 100vh;
}

/* 响应式 */
@media (width <= 768px) {
  .plyr-video {
    max-height: 320px;
  }
}
</style>
