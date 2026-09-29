function pad(value) {
  return String(value).padStart(2, '0')
}

function localParts(value) {
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) {
    return null
  }
  return {
    sale_start_date: `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`,
    sale_start_time: `${pad(date.getHours())}:${pad(date.getMinutes())}`
  }
}

function defaults() {
  return localParts(new Date(Date.now() + 60 * 60 * 1000))
}

function toForm(saleStartsAt) {
  const parts = saleStartsAt ? localParts(saleStartsAt) : defaults()
  return Object.assign({ schedule_enabled: Boolean(saleStartsAt) }, parts || defaults())
}

function toISO(form) {
  if (!form.schedule_enabled) {
    return null
  }
  const dateParts = String(form.sale_start_date || '').split('-').map(Number)
  const timeParts = String(form.sale_start_time || '').split(':').map(Number)
  if (dateParts.length !== 3 || timeParts.length !== 2 || dateParts.some(Number.isNaN) || timeParts.some(Number.isNaN)) {
    return ''
  }
  const value = new Date(dateParts[0], dateParts[1] - 1, dateParts[2], timeParts[0], timeParts[1], 0, 0)
  return Number.isNaN(value.getTime()) ? '' : value.toISOString()
}

function format(saleStartsAt) {
  const parts = saleStartsAt ? localParts(saleStartsAt) : null
  return parts ? `${parts.sale_start_date} ${parts.sale_start_time}` : ''
}

function isFuture(saleStartsAt) {
  return Boolean(saleStartsAt) && new Date(saleStartsAt).getTime() > Date.now()
}

module.exports = {
  defaults,
  toForm,
  toISO,
  format,
  isFuture
}
