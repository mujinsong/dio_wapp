const shop = require('../../utils/shop')
const operationRequest = require('../../utils/operationRequest')

function normalizeToken(raw) {
  const text = (raw || '').trim()
  if (!text) {
    return ''
  }
  const prefix = 'dio_redeem:'
  if (text.indexOf(prefix) === 0) {
    return text.slice(prefix.length)
  }
  return text
}

function formatTime(raw) {
  if (!raw) {
    return '-'
  }
  const date = new Date(raw)
  if (!Number.isFinite(date.getTime())) {
    return '-'
  }
  const pad = value => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function presentResult(result) {
  const order = result.order || {}
  return Object.assign({}, result, {
    goods_name: order.goods_name || '-',
    group_name: order.group_name || '默认团体',
    order_no: order.order_no || '-',
    points_text: `${order.points_cost || 0} PTS`,
    redeemed_text: formatTime(result.redeemed_at || order.redeemed_at)
  })
}

Page({
  data: {
    rawScanText: '',
    redeemToken: '',
    loading: false,
    pendingRedeemRequest: null,
    result: null,
    error: ''
  },

  handleTokenInput(event) {
    const rawScanText = event.detail.value || ''
    this.setData({
      rawScanText,
      redeemToken: normalizeToken(rawScanText),
      result: null,
      error: ''
    })
  },

  scanCode() {
    wx.scanCode({
      onlyFromCamera: true,
      scanType: ['qrCode'],
      success: res => {
        const rawScanText = res.result || ''
        this.setData({
          rawScanText,
          redeemToken: normalizeToken(rawScanText),
          result: null,
          error: ''
        })
      },
      fail: err => {
        const message = err && err.errMsg && err.errMsg.indexOf('cancel') >= 0 ? '已取消扫码' : '扫码失败'
        wx.showToast({ title: message, icon: 'none' })
      }
    })
  },

  submit() {
    if (this.data.loading) {
      return
    }
    if (!this.data.redeemToken) {
      wx.showToast({ title: '请先扫描核销码', icon: 'none' })
      return
    }

    const operation = operationRequest.resolve('redeem', this.data.pendingRedeemRequest, {
      redeem_token: this.data.redeemToken
    })
    this.setData({ loading: true, error: '', result: null, pendingRedeemRequest: operation })
    shop.redeemOrder(this.data.redeemToken, operation.id)
      .then(result => {
        this.setData({
          result: presentResult(result),
          rawScanText: '',
          redeemToken: '',
          pendingRedeemRequest: null
        })
        wx.showToast({ title: '核销成功', icon: 'success' })
      })
      .catch(err => {
		const messages = {
		  order_expired: '订单已超过核销期限，积分已自动退回',
		  order_already_redeemed: '该订单已核销',
		  redeem_token_not_found: '核销码无效或已刷新',
		  order_not_pending: '当前订单不能核销'
		}
        const raw = err.message || ''
		const message = messages[err && err.code] || (raw === 'exchange order was already redeemed'
          ? '该订单已核销'
		  : (raw === 'redeem QR code is invalid' ? '核销码无效或已刷新' : (raw || '核销失败')))
        this.setData({ error: message })
        wx.showToast({ title: message, icon: 'none' })
      })
      .finally(() => {
        this.setData({ loading: false })
      })
  }
})
