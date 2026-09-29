const points = require('../../utils/points')

Page({
  data: {
    qr: null,
    qrImage: '',
    loading: false,
    error: '',
    expired: false,
    expiresText: ''
  },

  onLoad() {
    this.createQRCode()
  },

  onShow() {
    if (this.data.qr && !this.data.loading) {
      this.startExpiryTimer(this.data.qr.expires_at)
    }
  },

  onHide() {
    this.clearExpiryTimer()
  },

  onUnload() {
    this.clearExpiryTimer()
    this.removeQRCodeFile(this.data.qrImage)
  },

  createQRCode() {
    if (this.data.loading) {
      return
    }
    const previousImage = this.data.qrImage
    this.clearExpiryTimer()
    this.setData({
      qr: null,
      qrImage: '',
      loading: true,
      error: '',
      expired: false,
      expiresText: ''
    })
    this.removeQRCodeFile(previousImage)
    points.createIdentityQRCode()
      .then(qr => {
        this.renderQRCode(qr)
        this.startExpiryTimer(qr && qr.expires_at)
      })
      .catch(err => {
        const message = err.message || '二维码生成失败'
        this.setData({ error: message })
        wx.showToast({ title: message, icon: 'none' })
      })
      .finally(() => {
        this.setData({ loading: false })
      })
  },

  renderQRCode(qr) {
    if (!qr || !qr.qr_image || qr.qr_image.indexOf('base64,') < 0) {
      this.setData({ qr, qrImage: qr && qr.qr_image ? qr.qr_image : '' })
      return
    }

    const base64Data = qr.qr_image.split('base64,')[1]
    const filePath = `${wx.env.USER_DATA_PATH}/identity-qr-${Date.now()}.png`
    wx.getFileSystemManager().writeFile({
      filePath,
      data: base64Data,
      encoding: 'base64',
      success: () => {
        const previousImage = this.data.qrImage
        this.setData({
          qr: Object.assign({}, qr, { qr_image_path: filePath }),
          qrImage: filePath
        })
        this.removeQRCodeFile(previousImage)
      },
      fail: () => {
        this.setData({ qr, qrImage: qr.qr_image })
      }
    })
  },

  startExpiryTimer(expiresAt) {
    this.clearExpiryTimer()
    const expiresAtMs = new Date(expiresAt).getTime()
    if (!Number.isFinite(expiresAtMs)) {
      this.setData({ expiresText: '短期有效' })
      return
    }

    const update = () => {
      const remainingSeconds = Math.max(0, Math.ceil((expiresAtMs - Date.now()) / 1000))
      if (remainingSeconds <= 0) {
        this.clearExpiryTimer()
        this.setData({ expired: true, expiresText: '已过期，请刷新' })
        return
      }
      const minutes = Math.floor(remainingSeconds / 60)
      const seconds = remainingSeconds % 60
      this.setData({
        expired: false,
        expiresText: `剩余 ${minutes}:${String(seconds).padStart(2, '0')}`
      })
    }

    update()
    if (!this.data.expired) {
      this.expiryTimer = setInterval(update, 1000)
    }
  },

  clearExpiryTimer() {
    if (this.expiryTimer) {
      clearInterval(this.expiryTimer)
      this.expiryTimer = null
    }
  },

  removeQRCodeFile(filePath) {
    if (!filePath || filePath.indexOf(wx.env.USER_DATA_PATH) !== 0) {
      return
    }
    wx.getFileSystemManager().unlink({
      filePath,
      fail: () => {}
    })
  }
})
