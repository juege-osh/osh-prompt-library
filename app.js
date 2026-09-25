const $ = id => document.getElementById(id);
const state = {page: 1, total: 0, size: 24, q: '', category: '', model: '', duration: '', asset: ''};
const promptCache = new Map();
let activeCaseId = '';
let activeVideo = null;
let activePromptButton = null;
let promptRequest = 0;
let loadRequest = 0;
let debounce;

function duration(value) {
  return Number.isFinite(value) && value > 0 ? `${value.toFixed(1)} 秒` : '时长待确认';
}

function optionList(select, items, selected) {
  const all = select.id === 'category' ? '全部类别' : '全部模型';
  select.replaceChildren(new Option(all, ''));
  for (const item of items) select.add(new Option(`${item.name} (${item.count})`, item.name));
  select.value = selected;
}

function hidePrompt() {
  promptRequest++;
  $('promptFloat').hidden = true;
  if (activePromptButton) activePromptButton.setAttribute('aria-expanded', 'false');
  activePromptButton = null;
  activeCaseId = '';
}

function positionPrompt() {
  if (!activePromptButton || $('promptFloat').hidden) return;
  const panel = $('promptFloat');
  const anchor = activePromptButton.getBoundingClientRect();
  const width = Math.min(440, window.innerWidth - 24);
  panel.style.width = `${width}px`;
  const left = Math.max(12, Math.min(anchor.left, window.innerWidth - width - 12));
  const below = anchor.bottom + 10 + panel.offsetHeight <= window.innerHeight - 12;
  const top = below ? anchor.bottom + 10 : Math.max(12, anchor.top - panel.offsetHeight - 10);
  panel.style.left = `${left}px`;
  panel.style.top = `${top}px`;
}

async function showPrompt(id, button) {
  if (activePromptButton === button && !$('promptFloat').hidden) {
    hidePrompt();
    return;
  }
  hidePrompt();
  activePromptButton = button;
  activeCaseId = id;
  button.setAttribute('aria-expanded', 'true');
  const panel = $('promptFloat');
  const text = $('promptText');
  const copy = $('copyPrompt');
  panel.hidden = false;
  text.textContent = '正在加载提示词…';
  $('promptImages').hidden = true;
  $('promptImageList').replaceChildren();
  $('assetStatus').textContent = '';
  $('downloadPack').disabled = true;
  $('copyAssets').hidden = true;
  copy.disabled = true;
  copy.textContent = '复制提示词';
  positionPrompt();
  const request = ++promptRequest;
  try {
    if (!promptCache.has(id)) {
      const response = await fetch(`/api/cases/${encodeURIComponent(id)}`);
      if (!response.ok) throw new Error('无法加载');
      const detail = await response.json();
      promptCache.set(id, {text: detail.prompt_zh || '', images: detail.input_images || [], missingReference: detail.missing_reference, hasOutputAudio: detail.has_output_audio});
    }
    if (request !== promptRequest) return;
    const detail = promptCache.get(id);
    text.textContent = detail.text || '暂无完整中文提示词';
    copy.disabled = !detail.text;
    $('downloadPack').disabled = !detail.text;
    $('copyAssets').hidden = !detail.images.length;
    $('copyAssets').disabled = !detail.text;
    $('downloadPack').textContent = detail.images.length ? '打包下载提示词和参考图' : '下载提示词';
    const status = [];
    if (detail.images.length) status.push(`已收录 ${detail.images.length} 张原始参考图`);
    else if (detail.missingReference) status.push('提示词提到的参考素材未收录');
    else status.push('暂无原始参考素材');
    status.push('未收录生成时使用的音色文件');
    if (detail.hasOutputAudio) status.push('成片有音轨，可随视频试听');
    $('assetStatus').textContent = status.join(' · ');
    if (detail.images.length) {
      const gallery = $('promptImageList');
      for (const url of detail.images) {
        const link = document.createElement('a');
        link.href = url;
        link.target = '_blank';
        link.rel = 'noopener noreferrer';
        link.setAttribute('aria-label', '查看原始参考图大图');
        const img = document.createElement('img');
        img.src = url;
        img.loading = 'lazy';
        img.alt = '原始输入图';
        link.append(img);
        gallery.append(link);
      }
      $('promptImages').hidden = false;
    }
  } catch {
    if (request !== promptRequest) return;
    text.textContent = '暂时无法加载提示词，请重试。';
  }
  positionPrompt();
}

function playCase(item, thumb) {
  if (!item.video) return;
  if (activeVideo) activeVideo.pause();
  hidePrompt();
  const video = document.createElement('video');
  video.controls = true;
  video.playsInline = true;
  video.preload = 'none';
  video.poster = item.poster || '';
  video.src = item.video;
  video.setAttribute('aria-label', item.title || '案例视频');
  thumb.replaceChildren(video);
  thumb.classList.add('isPlaying');
  activeVideo = video;
  let retry = 0;
  video.addEventListener('error', () => {
    if (retry++ > 0) return;
    const time = Number.isFinite(video.currentTime) ? video.currentTime : 0;
    video.src = `${item.video}?renew=${Date.now()}`;
    video.addEventListener('loadedmetadata', () => {
      if (time > 0) video.currentTime = time;
    }, {once: true});
    video.load();
  });
  video.play().catch(() => {});
}

function makeCard(item) {
  const card = document.createElement('article');
  card.className = 'card';
  const thumb = document.createElement('div');
  thumb.className = 'thumb';
  const cover = document.createElement('button');
  cover.className = 'thumbButton';
  cover.type = 'button';
  cover.setAttribute('aria-label', `播放视频：${item.title || '未命名案例'}`);
  if (item.poster) {
    const img = document.createElement('img');
    img.src = item.poster;
    img.loading = 'lazy';
    img.decoding = 'async';
    img.alt = '';
    cover.append(img);
  } else {
    const placeholder = document.createElement('span');
    placeholder.className = 'placeholder';
    placeholder.textContent = '✦';
    cover.append(placeholder);
  }
  const play = document.createElement('span');
  play.className = 'playMark';
  play.textContent = '▶';
  play.setAttribute('aria-hidden', 'true');
  const time = document.createElement('span');
  time.className = 'time';
  time.textContent = duration(item.duration);
  time.title = `片长 ${duration(item.duration)}`;
  cover.append(play, time);
  cover.disabled = !item.video;
  cover.addEventListener('click', () => playCase(item, thumb));
  thumb.append(cover);

  const body = document.createElement('div');
  body.className = 'cardBody';
  const title = document.createElement('h2');
  title.textContent = item.title || '未命名案例';
  const tags = document.createElement('div');
  tags.className = 'tags';
  for (const value of [item.category, item.style].filter(Boolean)) {
    const tag = document.createElement('span');
    tag.textContent = value;
    tags.append(tag);
  }
  const model = document.createElement('p');
  model.textContent = item.model || '模型待确认';
  if (item.input_image_count > 0 && item.input_image_preview) {
    const reference = document.createElement('button');
    reference.className = 'referencePreview';
    reference.type = 'button';
    reference.setAttribute('aria-label', `查看 ${item.input_image_count} 张原始参考图和提示词`);
    const image = document.createElement('img');
    image.src = item.input_image_preview;
    image.loading = 'lazy';
    image.decoding = 'async';
    image.alt = '原始参考图预览';
    const label = document.createElement('span');
    label.textContent = `原始参考图 ${item.input_image_count} 张 · 点击查看`;
    reference.append(image, label);
    reference.addEventListener('click', () => showPrompt(item.id, reference));
    body.append(title, tags, model, reference);
  } else {
    body.append(title, tags, model);
  }
  const promptButton = document.createElement('button');
  promptButton.className = 'promptAction';
  promptButton.type = 'button';
  promptButton.textContent = '查看';
  promptButton.setAttribute('aria-label', '查看提示词');
  promptButton.setAttribute('aria-expanded', 'false');
  promptButton.setAttribute('aria-controls', 'promptFloat');
  promptButton.addEventListener('click', () => showPrompt(item.id, promptButton));
  body.append(promptButton);
  card.append(thumb, body);
  return card;
}

async function load() {
  const request = ++loadRequest;
  hidePrompt();
  if (activeVideo) activeVideo.pause();
  activeVideo = null;
  const params = new URLSearchParams(state);
  const response = await fetch(`/api/cases?${params}`);
  if (!response.ok) throw new Error('加载失败');
  const data = await response.json();
  if (request !== loadRequest) return;
  state.total = data.total;
  state.size = data.size;
  $('accepted').textContent = data.accepted.toLocaleString();
  $('imageCases').textContent = data.image_cases.toLocaleString();
  $('missingCases').textContent = data.missing_cases.toLocaleString();
  for (const button of document.querySelectorAll('[data-asset]')) button.classList.toggle('selected', button.dataset.asset === state.asset);
  $('resultCount').textContent = `找到 ${data.total.toLocaleString()} 条案例`;
  optionList($('category'), data.categories, state.category);
  optionList($('model'), data.models, state.model);
  const grid = $('grid');
  grid.replaceChildren(...data.items.map(makeCard));
  if (!data.items.length) {
    const empty = document.createElement('div');
    empty.className = 'empty';
    empty.textContent = '没有找到符合条件的案例，换个关键词试试。';
    grid.append(empty);
  }
  const pages = Math.max(1, Math.ceil(data.total / state.size));
  $('pageText').textContent = `第 ${state.page} / ${pages} 页`;
  $('prev').disabled = state.page <= 1;
  $('next').disabled = state.page >= pages;
}

function showError() {
  const error = document.createElement('div');
  error.className = 'empty';
  error.textContent = '暂时无法加载案例，请刷新页面重试。';
  $('grid').replaceChildren(error);
}

for (const id of ['category', 'model', 'duration']) {
  $(id).addEventListener('change', () => {
    state[id] = $(id).value;
    state.page = 1;
    load().catch(showError);
  });
}
$('search').addEventListener('input', () => {
  clearTimeout(debounce);
  debounce = setTimeout(() => {
    state.q = $('search').value.trim();
    state.page = 1;
    load().catch(showError);
  }, 280);
});
$('clear').addEventListener('click', () => {
  state.q = state.category = state.model = state.duration = state.asset = '';
  state.page = 1;
  $('search').value = '';
  $('duration').value = '';
  load().catch(showError);
});
for (const button of document.querySelectorAll('[data-asset]')) {
  button.addEventListener('click', () => {
    state.asset = button.dataset.asset;
    state.page = 1;
    load().catch(showError);
  });
}
$('prev').addEventListener('click', () => {
  state.page--;
  load().catch(showError);
  scrollTo({top: 0, behavior: 'smooth'});
});
$('next').addEventListener('click', () => {
  state.page++;
  load().catch(showError);
  scrollTo({top: 0, behavior: 'smooth'});
});
$('closePrompt').addEventListener('click', hidePrompt);
$('copyPrompt').addEventListener('click', async () => {
  const detail = promptCache.get(activeCaseId);
  if (!detail?.text) return;
  await navigator.clipboard.writeText(detail.text);
  $('copyPrompt').textContent = '已复制';
  setTimeout(() => {$('copyPrompt').textContent = '复制提示词';}, 1400);
});
$('copyAssets').addEventListener('click', async () => {
  const detail = promptCache.get(activeCaseId);
  if (!detail?.text || !detail.images.length) return;
  const lines = ['完整中文提示词', detail.text, '', '原始参考图链接（打开时由网站签发临时地址）'];
  detail.images.forEach((url, index) => lines.push(`${index + 1}. ${new URL(url, location.origin).href}`));
  await navigator.clipboard.writeText(lines.join('\n'));
  $('assetStatus').textContent = '已复制提示词和素材链接；原文件请用“下载资料”保存。';
  $('copyAssets').textContent = '已复制提示词和素材链接';
  setTimeout(() => {$('copyAssets').textContent = '复制全部资产';}, 1400);
});
const encoder = new TextEncoder();
const crcTable = Uint32Array.from({length: 256}, (_, n) => {
  for (let i = 0; i < 8; i++) n = n & 1 ? 0xedb88320 ^ (n >>> 1) : n >>> 1;
  return n >>> 0;
});
function crc32(bytes) {
  let value = 0xffffffff;
  for (const byte of bytes) value = crcTable[(value ^ byte) & 255] ^ (value >>> 8);
  return (value ^ 0xffffffff) >>> 0;
}
function zipFiles(files) {
  const chunks = [], records = [];
  let offset = 0;
  for (const file of files) {
    const name = encoder.encode(file.name), data = file.data;
    const crc = crc32(data);
    const local = new Uint8Array(30 + name.length), l = new DataView(local.buffer);
    l.setUint32(0, 0x04034b50, true);
    l.setUint16(4, 20, true);
    l.setUint16(6, 0x0800, true);
    l.setUint32(14, crc, true);
    l.setUint32(18, data.length, true);
    l.setUint32(22, data.length, true);
    l.setUint16(26, name.length, true);
    local.set(name, 30);
    chunks.push(local, data);
    records.push({name, data, crc, offset});
    offset += local.length + data.length;
  }
  const directoryOffset = offset;
  for (const record of records) {
    const central = new Uint8Array(46 + record.name.length), c = new DataView(central.buffer);
    c.setUint32(0, 0x02014b50, true);
    c.setUint16(4, 20, true);
    c.setUint16(6, 20, true);
    c.setUint16(8, 0x0800, true);
    c.setUint32(16, record.crc, true);
    c.setUint32(20, record.data.length, true);
    c.setUint32(24, record.data.length, true);
    c.setUint16(28, record.name.length, true);
    c.setUint32(42, record.offset, true);
    central.set(record.name, 46);
    chunks.push(central);
    offset += central.length;
  }
  const end = new Uint8Array(22), e = new DataView(end.buffer);
  e.setUint32(0, 0x06054b50, true);
  e.setUint16(8, records.length, true);
  e.setUint16(10, records.length, true);
  e.setUint32(12, offset - directoryOffset, true);
  e.setUint32(16, directoryOffset, true);
  chunks.push(end);
  return new Blob(chunks, {type: 'application/zip'});
}
function saveFile(blob, filename) {
  const url = URL.createObjectURL(blob), link = document.createElement('a');
  link.href = url;
  link.download = filename;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 60000);
}
$('downloadPack').addEventListener('click', async () => {
  const detail = promptCache.get(activeCaseId);
  if (!detail?.text) return;
  const id = activeCaseId;
  const button = $('downloadPack');
  button.disabled = true;
  button.textContent = '正在准备下载…';
  try {
    const prompt = encoder.encode(detail.text + '\n');
    if (!detail.images.length) {
      saveFile(new Blob([prompt], {type: 'text/plain;charset=utf-8'}), `${id}-提示词.txt`);
      return;
    }
    const files = [{name: '提示词.txt', data: prompt}];
    for (const [index, url] of detail.images.entries()) {
      const response = await fetch(url);
      if (!response.ok) throw new Error('参考图下载失败');
      const type = response.headers.get('content-type') || '';
      const ext = type.includes('png') ? 'png' : type.includes('webp') ? 'webp' : type.includes('gif') ? 'gif' : 'jpg';
      files.push({name: `参考图-${String(index + 1).padStart(2, '0')}.${ext}`, data: new Uint8Array(await response.arrayBuffer())});
    }
    saveFile(zipFiles(files), `${id}-提示词与参考图.zip`);
  } catch {
    $('assetStatus').textContent = '打包失败，请稍后重试。';
  } finally {
    button.disabled = false;
    button.textContent = detail.images.length ? '打包下载提示词和参考图' : '下载提示词';
  }
});
document.addEventListener('pointerdown', event => {
  if (!$('promptFloat').hidden && !$('promptFloat').contains(event.target) && event.target !== activePromptButton) hidePrompt();
});
document.addEventListener('keydown', event => {if (event.key === 'Escape') hidePrompt();});
window.addEventListener('resize', positionPrompt);
window.addEventListener('scroll', hidePrompt, {passive: true});
load().catch(showError);
