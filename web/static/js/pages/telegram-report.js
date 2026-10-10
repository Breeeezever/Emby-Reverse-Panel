// Telegram administration and scheduled reports share one encrypted credential.
let telegramReportLoaded = false;
let telegramStatusTimer = null;
let telegramTokenConfigured = false;
let telegramSubmitBusy = false;

function renderTelegramReport() {
  clearTimeout(telegramStatusTimer);
  telegramReportLoaded = false;
  const page = document.getElementById('page-telegram-report');
  if (!page) return;
  page.innerHTML = `
    <div class="settings-layout fade-up">
      ${globalSettingsNav('telegram')}
      <main class="settings-content">
        <section class="telegram-report-card telegram-bot-card">
          <div class="telegram-report-card-head">
            <h2>连接机器人</h2>
            <span class="telegram-bot-status" id="telegram-bot-status" role="status">正在读取…</span>
          </div>
          <div class="telegram-secret-warning" id="telegram-secret-warning" hidden>
            请先配置固定的 JWT_SECRET，才能安全保存 Bot Token。
          </div>
          <label class="telegram-field">
            <span>Bot Token</span>
            <input class="form-input" type="password" id="telegram-bot-token" autocomplete="new-password" placeholder="从 @BotFather 获取" maxlength="256">
            <small id="telegram-token-state">尚未配置</small>
          </label>
          <p class="form-help telegram-bot-connection" id="telegram-bot-connection" aria-live="polite"></p>
        </section>

        <section class="telegram-report-card telegram-bot-card">
          <div class="telegram-report-card-head">
            <h2>管理员操作</h2>
            <label class="telegram-switch"><input type="checkbox" id="telegram-bot-enabled"><span aria-hidden="true"></span><strong>启用管理</strong></label>
          </div>
          <div class="telegram-report-form-grid" id="telegram-admin-fields">
            <label class="telegram-field telegram-field-wide">
              <span>管理员 Telegram ID</span>
              <input class="form-input" id="telegram-admin-ids" autocomplete="off" placeholder="例如 123456789，多个 ID 用逗号分隔" maxlength="200">
              <small>填写数字用户 ID；仅允许这些管理员在私聊中操作。</small>
            </label>
            <div class="telegram-field telegram-field-wide">
              <label for="telegram-public-url">面板外部地址</label>
              <div class="telegram-url-row">
                <input class="form-input" type="url" id="telegram-public-url" autocomplete="url" placeholder="https://panel.example.com:8443" maxlength="512">
                <button class="telegram-btn secondary" type="button" id="telegram-use-current">使用当前地址</button>
              </div>
              <small>Bot 通过这个地址生成站点入口，需包含实际访问端口。</small>
            </div>
          </div>
          <details class="telegram-report-details">
            <summary>Bot 操作说明</summary>
            <div class="telegram-bot-help">
              <p>保存后向 Bot 私聊发送 <code>/start</code>，即可通过按钮操作。</p>
              <p>支持立即日报、站点地址与流量、添加和编辑站点、启停、删除及连接检测。修改前会显示确认信息。</p>
              <p><code>/cancel</code> 取消当前操作；<code>/menu</code> 返回主菜单。</p>
            </div>
          </details>
        </section>

        <section class="telegram-report-card telegram-bot-card">
          <div class="telegram-report-card-head">
            <h2>定时日报</h2>
            <label class="telegram-switch"><input type="checkbox" id="telegram-report-enabled"><span aria-hidden="true"></span><strong>启用日报</strong></label>
          </div>
          <div class="telegram-report-form-grid">
            <label class="telegram-field telegram-field-wide">
              <span>接收 Chat ID</span>
              <input class="form-input" type="text" id="telegram-chat-id" autocomplete="off" placeholder="个人、群组或频道 Chat ID" maxlength="128">
              <small>仅用于推送日报，不授予管理权限。</small>
            </label>
            <label class="telegram-field"><span>通知频率</span>
              <select class="form-input" id="telegram-frequency"><option value="daily">每天</option><option value="weekly">每周</option></select>
            </label>
            <label class="telegram-field" id="telegram-weekday-field" hidden><span>每周发送日</span>
              <select class="form-input" id="telegram-weekday">
                <option value="1">星期一</option><option value="2">星期二</option><option value="3">星期三</option><option value="4">星期四</option><option value="5">星期五</option><option value="6">星期六</option><option value="0">星期日</option>
              </select>
            </label>
            <label class="telegram-field"><span>发送时间</span>
              <input class="form-input" type="time" id="telegram-schedule-time" value="20:00" required>
              <small>按全局设置中的时区发送。</small>
            </label>
          </div>
        </section>
        <div class="telegram-report-actions telegram-bot-actions">
          <button class="telegram-btn secondary" type="button" id="telegram-test" disabled>立即发送日报</button>
          <button class="telegram-btn primary" type="button" id="telegram-save" disabled>保存设置</button>
        </div>
      </main>
    </div>`;
  bindGlobalSettingsNav(page);
  bindFieldValidation(page);
  document.getElementById('telegram-frequency').onchange = updateTelegramFrequencyFields;
  document.getElementById('telegram-use-current').onclick = () => {
    document.getElementById('telegram-public-url').value = window.location.origin;
    setFieldError(document.getElementById('telegram-public-url'), '');
  };
  document.getElementById('telegram-save').onclick = () => submitTelegramReport(false);
  document.getElementById('telegram-test').onclick = () => submitTelegramReport(true);
  loadTelegramReportSettings();
}

function updateTelegramFrequencyFields() {
  const weekly = document.getElementById('telegram-frequency').value === 'weekly';
  document.getElementById('telegram-weekday-field').hidden = !weekly;
  document.getElementById('telegram-weekday').disabled = !weekly;
}

function showTelegramBotStatus(settings) {
  const element = document.getElementById('telegram-bot-status');
  if (!element) return;
  const bot = settings.bot || {};
  element.textContent = ({ online: '已连接', error: '连接异常', connecting: '连接中', disabled: '未启用管理' })[bot.status] || '未启用管理';
  element.dataset.state = bot.status || 'disabled';
  document.getElementById('telegram-bot-connection').textContent = bot.enabled ? (bot.status_message || '') : '';
}

function scheduleTelegramBotStatus() {
  clearTimeout(telegramStatusTimer);
  const current = document.getElementById('telegram-bot-status');
  telegramStatusTimer = setTimeout(async () => {
    if (Router.current !== 'telegram-report' || document.getElementById('telegram-bot-status') !== current) return;
    try {
      const settings = await API.getTelegramReportSettings();
      if (Router.current !== 'telegram-report' || document.getElementById('telegram-bot-status') !== current) return;
      showTelegramBotStatus(settings);
    } catch (_) {
      if (document.getElementById('telegram-bot-status') === current) current.textContent = '状态暂不可用';
    }
    if (Router.current === 'telegram-report') scheduleTelegramBotStatus();
  }, 10000);
}

async function loadTelegramReportSettings() {
  try {
    const settings = await API.getTelegramReportSettings();
    if (!settings || Router.current !== 'telegram-report') return;
    const bot = settings.bot || {};
    telegramReportLoaded = true;
    telegramTokenConfigured = !!settings.bot_token_configured;
    document.getElementById('telegram-report-enabled').checked = !!settings.enabled;
    document.getElementById('telegram-bot-enabled').checked = !!bot.enabled;
    document.getElementById('telegram-bot-token').value = '';
    document.getElementById('telegram-bot-token').placeholder = telegramTokenConfigured ? '留空保留已保存的 Token' : '从 @BotFather 获取';
    document.getElementById('telegram-admin-ids').value = (bot.admin_ids || []).join(', ');
    document.getElementById('telegram-public-url').value = bot.public_url || window.location.origin;
    document.getElementById('telegram-chat-id').value = settings.chat_id || '';
    document.getElementById('telegram-frequency').value = settings.frequency || 'daily';
    document.getElementById('telegram-weekday').value = String(Number.isInteger(settings.weekday) ? settings.weekday : 1);
    document.getElementById('telegram-schedule-time').value = settings.schedule_time || '20:00';
    document.getElementById('telegram-token-state').textContent = telegramTokenConfigured ? 'Token 已加密保存' : '尚未配置';
    document.getElementById('telegram-secret-warning').hidden = settings.secret_stable !== false;
    document.getElementById('telegram-save').disabled = false;
    document.getElementById('telegram-test').disabled = false;
    showTelegramBotStatus(settings);
    updateTelegramFrequencyFields();
    scheduleTelegramBotStatus();
  } catch (error) {
    Toast.error('读取 Telegram 设置失败：' + error.message);
  }
}

function parseTelegramAdminIDs(raw) {
  const values = String(raw).trim().split(/[\s,，]+/).filter(Boolean);
  if (values.length > 10 || values.some(value => !/^[1-9]\d*$/.test(value) || !Number.isSafeInteger(Number(value)) || Number(value) > 4503599627370495)) return null;
  return [...new Set(values.map(Number))];
}

function telegramReportPayload(action) {
  const base = {
    bot_token: document.getElementById('telegram-bot-token').value.trim(),
    chat_id: document.getElementById('telegram-chat-id').value.trim(),
  };
  if (action) return { ...base, action };
  return {
    ...base,
    enabled: document.getElementById('telegram-report-enabled').checked,
    frequency: document.getElementById('telegram-frequency').value,
    weekday: Number(document.getElementById('telegram-weekday').value),
    schedule_time: document.getElementById('telegram-schedule-time').value,
    bot: {
      enabled: document.getElementById('telegram-bot-enabled').checked,
      admin_ids: parseTelegramAdminIDs(document.getElementById('telegram-admin-ids').value),
      public_url: document.getElementById('telegram-public-url').value.trim().replace(/\/+$/, ''),
    },
  };
}

function validateTelegramForm(payload, testOnly) {
  const reject = (id, message) => { const field = document.getElementById(id); setFieldError(field, message); field.focus(); return false; };
  document.querySelectorAll('#page-telegram-report input').forEach(field => setFieldError(field, ''));
  if ((testOnly || payload.enabled || payload.bot?.enabled) && !payload.bot_token && !telegramTokenConfigured) return reject('telegram-bot-token', '请先填写 Bot Token');
  if ((testOnly || payload.enabled) && !payload.chat_id) return reject('telegram-chat-id', '请填写日报接收 Chat ID');
  if (testOnly) return true;
  if (payload.bot.admin_ids === null || (payload.bot.enabled && !payload.bot.admin_ids.length)) return reject('telegram-admin-ids', '请填写有效的数字用户 ID，最多 10 个；不能使用群组 ID');
  if (payload.bot.public_url || payload.bot.enabled) {
    let valid = false;
    try { const u = new URL(payload.bot.public_url); valid = ['http:', 'https:'].includes(u.protocol) && !!u.hostname && !u.username && !u.password && !u.search && !u.hash && u.pathname === '/' && (!u.port || (Number(u.port) >= 1 && Number(u.port) <= 65535)); } catch (_) {}
    if (!valid) return reject('telegram-public-url', '请填写面板的 HTTP/HTTPS 外部地址，不包含路径、账号或参数');
  }
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(payload.schedule_time)) return reject('telegram-schedule-time', '请选择有效的发送时间');
  return true;
}

async function submitTelegramReport(testOnly) {
  if (!telegramReportLoaded || telegramSubmitBusy) return;
  const payload = telegramReportPayload(testOnly ? 'test' : '');
  if (!validateTelegramForm(payload, testOnly)) return;
  const button = document.getElementById(testOnly ? 'telegram-test' : 'telegram-save');
  const original = button.textContent;
  telegramSubmitBusy = true;
  ['telegram-test', 'telegram-save'].forEach(id => document.getElementById(id).disabled = true);
  button.textContent = testOnly ? '正在发送…' : '正在保存…';
  try {
    const result = await API.saveTelegramReportSettings(payload);
    if (testOnly) Toast.success('日报已发送');
    else {
      Toast.success('Telegram 设置已保存');
      if (Router.current === 'telegram-report' && document.getElementById('telegram-save') === button) await loadTelegramReportSettings();
    }
    return result;
  } catch (error) {
    Toast.error((testOnly ? '发送失败：' : '保存失败：') + error.message);
  } finally {
    telegramSubmitBusy = false;
    if (button.isConnected) {
      ['telegram-test', 'telegram-save'].forEach(id => { const element = document.getElementById(id); if (element) element.disabled = !telegramReportLoaded; });
      button.textContent = original;
    }
  }
}
