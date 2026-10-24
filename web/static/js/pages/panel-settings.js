// Panel endpoint validation, certificate setup and restart workflow.

function currentPanelAccessPort(location = typeof window !== 'undefined' ? window.location : null) {
  if (!location || !['http:', 'https:'].includes(location.protocol)) return null;
  return parseBoundedInteger(location.port || (location.protocol === 'https:' ? '443' : '80'), 1, 65535);
}

function panelEndpointFormPort(status, location) {
  const saved = parseBoundedInteger(status.listen_port, 1, 65535);
  // This input changes the server listener, never the external proxy's port.
  return saved || parseBoundedInteger(status.active_listen_port, 1, 65535) || currentPanelAccessPort(location) || 9090;
}

function panelEndpointValidationHTML(validation) {
  const title = !validation.can_save ? '此配置不能保存'
    : !validation.can_restart ? '可保存待应用设置，重启条件尚未满足'
    : '配置检查通过';
  const checks = Array.isArray(validation.checks) ? validation.checks : [];
  return `<strong>${esc(title)}</strong><ul>${checks.map(check => {
    const state = ['ok', 'warning', 'blocked'].includes(check.state) ? check.state : 'warning';
    const label = state === 'ok' ? '通过' : state === 'blocked' && !check.restart_only ? '阻止' : '待处理';
    return `<li class="panel-endpoint-check ${state}${check.restart_only ? ' restart-only' : ''}"><span>${label}</span><div>${esc(check.message || '')}</div></li>`;
  }).join('')}</ul>`;
}

async function renderTLSSettings() {
  const page = document.getElementById('page-settings-tls');
  if (!page) return;
  page.innerHTML = `<div class="settings-layout fade-up">${globalSettingsNav('tls')}<main class="settings-content"><div class="settings-loading">正在读取 TLS 配置…</div></main></div>`;
  bindGlobalSettingsNav(page);
  try {
    const status = await API.panelCertificate();
    if (Router.current !== 'settings-tls') return;
    const savedTLS = status.saved_tls_enabled === true;
    const activeURL = window.location.origin;
    const portHelp = status.external_proxy
      ? '这是反向代理的内部回源端口，不能只在面板修改。需要切换时请使用安装器或同步调整部署配置。'
      : '保存后需应用并重启。新端口还需在云防火墙或容器映射中放行。';
    page.innerHTML = `<div class="settings-layout fade-up">${globalSettingsNav('tls')}<main class="settings-content">
      <section class="settings-panel" id="p-endpoint-settings">
        <header><h2>面板访问地址</h2></header>
        <div class="panel-endpoint-current"><span>当前访问地址</span><strong class="mono">${esc(activeURL)}</strong><small>${status.external_proxy ? '外部反向代理管理入口' : status.tls_enabled ? 'Meridian 原生 HTTPS 已启用' : 'Meridian HTTP'}</small></div>
        ${status.external_proxy || !status.active_access_url ? '' : `<p class="form-help">Meridian 监听地址：<span class="mono">${esc(status.active_access_url)}</span>。经由代理访问时，浏览器地址和证书可能不同；原生端口需单独放行。</p>`}
        <p class="form-help" id="p-endpoint-state"></p>
        <div class="global-settings-status panel-certificate-status">${renderPanelCertificateStatus(status)}</div>
        <div class="settings-grid" style="margin-top:20px">
          <label class="settings-field"><span>面板域名</span><input class="form-input" id="p-panel-domain" value="${esc(status.panel_domain || '')}" placeholder="panel.example.com" required autocomplete="off" autocapitalize="none" spellcheck="false"><small>不含协议和端口。</small></label>
          <label class="settings-field"><span>节点泛域名（可选）</span><input class="form-input" id="p-wildcard-domain" value="${esc(status.wildcard_domain || '')}" placeholder="*.example.com" autocomplete="off" autocapitalize="none" spellcheck="false"><small>使用域名前缀入口或申请泛域名证书时填写。</small></label>
          <label class="settings-field"><span>面板监听端口</span><input class="form-input" id="p-panel-listen-port" type="number" min="1" max="65535" step="1" inputmode="numeric" required value="${esc(String(panelEndpointFormPort(status)))}"><small>${esc(portHelp)}</small></label>
          <div class="settings-field"><span>应用后的访问地址</span><div class="form-help mono" id="p-panel-address-preview"></div></div>
        </div>
        <div class="settings-save-bar tls-settings-actions"><button class="telegram-btn" id="p-endpoint-check" type="button">检查配置</button><button class="telegram-btn primary" id="p-cert-save" type="button">检查并保存</button></div>
        <div class="panel-endpoint-validation" id="p-endpoint-validation" role="status" aria-live="polite"></div>
        <p class="form-help">检查解析、代理端口、监听、站点冲突与证书条件；检查通过不代表云防火墙或外部转发已放行。</p>
        <div class="settings-save-bar" ${status.restart_required ? '' : 'hidden'}><button class="telegram-btn primary" id="p-cert-restart" type="button" disabled>应用设置并重启</button></div>
      </section>
      <section class="settings-panel">
        <header><h2>${status.external_proxy ? '当前证书由外部代理管理' : '配置原生 HTTPS 证书'}</h2></header>
        <p class="settings-panel-help">${status.external_proxy
          ? '当前 HTTPS 由 Nginx、Caddy 或其他网关提供，证书需在该代理中管理。只添加路径前缀（例如 /cinema/）可直接复用现有地址和证书，无需申请泛域名证书。使用域名前缀时，代理还需配置对应的泛域名证书、DNS 和转发规则。'
          : '路径前缀复用现有面板证书，无需额外申请。只有使用不同子域名的域名前缀入口，才需要覆盖这些域名的证书。'}</p>
        <div ${status.external_proxy ? 'hidden' : ''}>
        <label class="settings-field"><span>证书来源</span><select class="form-select" id="p-cert-source"><option value="acme">自动申请（Cloudflare DNS）</option><option value="path">服务器文件路径</option><option value="upload">上传证书与私钥</option></select></label>
        <div id="p-source-acme" class="tls-source-fields">
          <p class="settings-panel-help">通过 DNS 验证申请泛域名证书，签发后自动续期，无需开放 80 端口。请先保存节点泛域名。</p>
          <label class="settings-field"><span>ACME 邮箱</span><input class="form-input" id="p-acme-email" type="email" value="${esc(status.acme_email || '')}" autocomplete="email" placeholder="admin@example.com"></label>
          <label class="settings-field"><span>Cloudflare DNS API Token</span><input class="form-input" id="p-acme-token" type="password" autocomplete="new-password" maxlength="512" placeholder="${status.dns_api_token_configured ? '已保存，留空继续使用' : '填写 DNS API Token'}"><small>需要对应域名的 Zone:Read 与 DNS:Edit 权限。</small></label>
          <label class="settings-check"><input id="p-acme-staging" type="checkbox"><span>测试签发（不替换当前证书）</span></label>
        </div>
        <div id="p-source-path" class="tls-source-fields" hidden>
          <p class="settings-panel-help">读取服务器上的 PEM 文件；外部续期后会自动重新加载。Meridian 服务账户需要文件读取权限。</p>
          <label class="settings-field"><span>完整证书链路径</span><input class="form-input" id="p-certificate-path" value="${esc(status.certificate_path || '')}" placeholder="/etc/letsencrypt/live/example.com/fullchain.pem" autocomplete="off" spellcheck="false"></label>
          <label class="settings-field"><span>私钥路径</span><input class="form-input" id="p-private-key-path" value="${esc(status.private_key_path || '')}" placeholder="/etc/letsencrypt/live/example.com/privkey.pem" autocomplete="off" spellcheck="false"></label>
        </div>
        <div id="p-source-upload" class="tls-source-fields" hidden>
          <p class="settings-panel-help">上传 PEM 格式的完整证书链和未加密私钥。更新证书时再次上传即可。</p>
          <label class="settings-field"><span>完整证书链文件</span><input class="form-input" id="p-certificate-file" type="file" accept=".pem,.crt,.cer"></label>
          <label class="settings-field"><span>私钥文件</span><input class="form-input" id="p-private-key-file" type="file" accept=".pem,.key"></label>
        </div>
        <div class="settings-save-bar tls-settings-actions">
          <button class="telegram-btn primary" id="p-cert-apply" type="button" ${status.issuing ? 'disabled' : ''}>应用证书</button>
        </div>
        <p class="form-help" id="p-cert-result" role="status"></p>
        </div>
      </section>
    </main></div>`;
    bindGlobalSettingsNav(page);
    const get = id => page.querySelector(`#p-${id}`);
    const validationElement = get('endpoint-validation');
    const stillCurrent = () => Router.current === 'settings-tls' && page.querySelector('#p-endpoint-validation') === validationElement;
    const source = get('cert-source');
    let busy = false, checkSequence = 0, validation = null;
    const savedPayload = {panel_domain: String(status.panel_domain || ''), wildcard_domain: String(status.wildcard_domain || ''), listen_port: Number(status.listen_port)};
    source.value = ['acme', 'path', 'upload'].includes(status.certificate_source) ? status.certificate_source : 'acme';
    const syncSource = () => {
      for (const name of ['acme', 'path', 'upload']) get(`source-${name}`).hidden = source.value !== name;
      get('cert-apply').textContent = source.value === 'acme' ? (get('acme-staging').checked ? '验证测试签发' : '申请并应用正式证书') : source.value === 'path' ? '读取并应用证书' : '上传并应用证书';
    };
    source.onchange = syncSource; get('acme-staging').onchange = syncSource; syncSource();
    const settingsPayload = () => ({panel_domain: get('panel-domain').value.trim(), wildcard_domain: get('wildcard-domain').value.trim(), listen_port: Number(get('panel-listen-port').value)});
    const sameSettings = (left, right) => left.panel_domain.toLowerCase() === right.panel_domain.toLowerCase() && left.wildcard_domain.toLowerCase() === right.wildcard_domain.toLowerCase() && left.listen_port === right.listen_port;
    const dirty = () => !sameSettings(settingsPayload(), savedPayload);
    const syncActions = () => {
      const unsaved = dirty();
      get('endpoint-state').textContent = unsaved ? '下方修改尚未保存，当前生效地址保持不变。'
        : status.restart_required ? '已有保存的设置等待应用；下方表单尚未替换当前生效地址。'
        : '下方为已保存设置。修改后先检查并保存，再应用并重启。';
      get('endpoint-state').classList.toggle('panel-endpoint-pending', unsaved || status.restart_required);
      get('cert-save').disabled = busy || Boolean(validation && !validation.can_save);
      get('endpoint-check').disabled = busy;
      get('cert-apply').disabled = busy || dirty() || status.issuing || status.external_proxy || !validation || !validation.can_save;
      get('cert-restart').disabled = busy || dirty() || !validation || !validation.can_restart || !status.restart_required;
      for (const id of ['panel-domain', 'wildcard-domain', 'panel-listen-port']) get(id).disabled = busy;
      get('cert-save').textContent = busy ? '处理中…' : '检查并保存';
    };
    const showValidation = result => {
      validation = result;
      get('endpoint-validation').innerHTML = panelEndpointValidationHTML(result);
      syncActions();
    };
    const showError = error => {
      if (error.validation) showValidation(error.validation);
      else { validation = null; get('endpoint-validation').textContent = error.message; syncActions(); }
    };
    const checkSettings = async () => {
      const sequence = ++checkSequence, payload = settingsPayload();
      validation = null;
      get('endpoint-validation').textContent = '正在检查配置…';
      syncActions();
      try {
        const result = await API.checkPanelSettings(payload);
        if (stillCurrent() && sequence === checkSequence && sameSettings(payload, settingsPayload())) showValidation(result);
      } catch (error) {
        if (stillCurrent() && sequence === checkSequence) showError(error);
      }
    };
    const refreshPreview = () => {
      const port = parseBoundedInteger(get('panel-listen-port').value, 1, 65535);
      const domain = get('panel-domain').value.trim();
      const protocol = status.external_proxy ? window.location.protocol : savedTLS ? 'https:' : 'http:';
      const publicPort = status.external_proxy ? currentPanelAccessPort() : port;
      const defaultPort = protocol === 'https:' ? 443 : 80;
      get('panel-address-preview').textContent = port === null ? '请填写有效端口（1–65535）' : domain ? `${protocol}//${domain}${publicPort === defaultPort ? '' : `:${publicPort}`}` : '—';
    };
    for (const id of ['panel-domain', 'wildcard-domain', 'panel-listen-port']) get(id).addEventListener('input', () => {
      checkSequence++; validation = null;
      get('endpoint-validation').textContent = '内容已修改，请重新检查并保存。';
      refreshPreview(); syncActions();
    });
    refreshPreview();
    get('endpoint-check').onclick = () => { if (validateFields(get('endpoint-settings'))) checkSettings(); };
    get('cert-save').onclick = async () => {
      const button = get('cert-save');
      if (button.disabled || !validateFields(get('endpoint-settings'))) return;
      checkSequence++; busy = true; syncActions();
      get('endpoint-validation').textContent = '正在检查并保存…';
      try { await API.savePanelSettings(settingsPayload()); window.location.reload(); }
      catch (error) { if (button.isConnected) { busy = false; showError(error); } }
    };
    get('cert-apply').onclick = async () => {
      const button = get('cert-apply'); if (button.disabled) return;
      const current = settingsPayload();
      if (current.panel_domain.toLowerCase() !== String(status.panel_domain || '').toLowerCase() || current.wildcard_domain.toLowerCase() !== String(status.wildcard_domain || '').toLowerCase() || current.listen_port !== Number(status.listen_port)) return Toast.error('请先保存域名与端口');
      let payload;
      if (source.value === 'acme') {
        if (!current.wildcard_domain) return Toast.error('申请泛域名证书前，请填写并保存节点泛域名');
        if (!get('acme-email').value.trim() || !get('acme-email').checkValidity()) return rejectField(get('acme-email'), '请填写有效邮箱');
        if (!get('acme-token').value.trim() && !status.dns_api_token_configured) return rejectField(get('acme-token'), '请填写 DNS API Token');
        payload = {email:get('acme-email').value.trim(),dns_provider:'cloudflare',dns_api_token:get('acme-token').value.trim(),staging:get('acme-staging').checked};
      } else if (source.value === 'path') {
        if (!get('certificate-path').value.trim() || !get('private-key-path').value.trim()) return Toast.error('请填写证书与私钥路径');
        payload = {source:'path',certificate_path:get('certificate-path').value.trim(),private_key_path:get('private-key-path').value.trim()};
      } else {
        const cert = get('certificate-file').files[0], key = get('private-key-file').files[0];
        if (!cert || !key) return Toast.error('请选择证书链和私钥文件');
        if (cert.size > 512*1024 || key.size > 64*1024) return Toast.error('证书不能超过 512 KB，私钥不能超过 64 KB');
        payload = {source:'upload', certFile:cert, keyFile:key};
      }
      checkSequence++; busy = true; syncActions();
      source.disabled = true; button.textContent = source.value === 'acme' ? '签发中，请稍候…' : '校验并应用中…';
      get('cert-result').textContent = source.value === 'acme' ? '正在申请证书，DNS 验证最多等待 2 分钟，签发流程设有 5 分钟超时，请勿重复提交。' : '正在校验并应用证书…';
      try {
        if (payload.source === 'upload') payload = {source:'upload',certificate_pem:await payload.certFile.text(),private_key_pem:await payload.keyFile.text()};
        const updated = source.value === 'acme' ? await API.requestPanelCertificate(payload) : await API.importPanelCertificate(payload);
        if (updated.test_issued) {
          busy = false; source.disabled = false; syncSource(); syncActions();
          get('cert-result').textContent = '测试签发通过，当前证书和自动续签配置未改变。取消“测试签发”后可申请正式证书。';
          Toast.success('测试签发通过');
          return;
        }
        Toast.success(updated.restart_required ? '证书已保存，请重启以启用 HTTPS' : '证书已应用');
        if (updated.restart_required) window.location.reload();
        else await renderTLSSettings();
      } catch (error) { if (button.isConnected) {busy=false;showError(error);get('cert-result').textContent='证书操作失败：'+error.message;source.disabled=false;syncSource();syncActions();} }
    };
    const restart = get('cert-restart');
    if (restart) restart.onclick = async () => {
      if (!window.confirm('重启会短暂中断面板与站点连接，确定继续吗？')) return;
      busy = true; checkSequence++; syncActions(); restart.textContent = '重启中…';
      try { const result = await API.restartSystem(); await waitForPanelRestart(result.redirect_url); }
      catch (error) { if(restart.isConnected){busy=false;showError(error);restart.textContent='应用设置并重启';} }
    };
    if (status.external_proxy) get('cert-result').textContent = '当前入口由外部反向代理提供 HTTPS，请在代理中管理证书。';
    syncActions();
    await checkSettings();
  } catch (error) {
    if (Router.current !== 'settings-tls') return;
    page.querySelector('.settings-content').innerHTML = `<div class="settings-loading request-log-error">读取 TLS 状态失败：${esc(error.message)}</div>`;
  }
}

function panelCertificateTime(value) {
  if (!value) return '尚无记录';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', {hour12: false});
}

function renderPanelCertificateStatus(status) {
  if (!status || status.available === false) {
    return '<div class="form-help" style="color:var(--orange)">TLS 数据目录不可用，请检查存储配置。</div>';
  }
  if (status.external_proxy) return '<div class="form-help">此页只管理 Meridian 原生监听证书；当前入口的证书有效期与信任状态请在外部代理中查看。路径前缀无需额外证书。</div>';
  const row = (label, value, state = '') => `<div class="diag-row"><span class="diag-key">${esc(label)}</span><span class="diag-val ${state}">${esc(value)}</span></div>`;
  const active = status.active_certificate;
  const renewal = status.renewal || {};
  const source = renewal.source || status.certificate_source;
  let result = '';
  if (active) {
    result += row('当前提供的证书', (active.dns_names || []).join('、') || '—');
    result += row('当前证书到期', panelCertificateTime(active.expires_at), active.valid ? '' : 'warn');
    result += row('当前证书信任', active.trust_message || '尚未校验', active.trusted ? 'good' : 'warn');
  }
  if (status.configured && (!active || status.restart_required || active.fingerprint !== status.certificate_fingerprint)) {
    result += row(status.restart_required ? '已保存的证书' : '证书覆盖域名', (status.certificate_dns_names || []).join('、') || status.subject || '—');
    result += row('已保存证书匹配', status.certificate_current ? '匹配已保存域名' : '需要申请或更新', status.certificate_current ? 'good' : 'warn');
    result += row('已保存证书到期', panelCertificateTime(status.expires_at), status.certificate_valid ? '' : 'warn');
    result += row('已保存证书信任', status.certificate_trust_message || '尚未校验', status.certificate_trusted ? 'good' : 'warn');
  } else if (status.configured) {
    result += row('域名覆盖', status.certificate_current ? '匹配面板与节点域名' : '需要申请或更新', status.certificate_current ? 'good' : 'warn');
  } else result += '<div class="form-help">尚未配置可读取且配对的证书</div>';
  const renewalText = source === 'path' ? '每分钟检查服务器文件' : source === 'upload' ? '需在到期前重新上传' : status.auto_renew_enabled ? '已启用，到期前 30 天续签' : '未启用，请检查正式证书、邮箱和 Token';
  result += row('后台证书维护', renewalText, source === 'upload' || status.auto_renew_enabled || source === 'path' ? '' : 'warn');
  if (renewal.pending) result += row('维护中的域名', `${renewal.domain || '当前入口'}；新配置等待应用`, 'warn');
  result += row('最近检查', panelCertificateTime(renewal.last_checked_at));
  result += row('最近更新成功', panelCertificateTime(renewal.last_success_at));
  if (renewal.last_error) result += row('最近维护失败', renewal.last_error, 'warn');
  if (source === 'path' || source === 'acme') result += row(renewal.last_error ? '下次重试' : '下次检查', panelCertificateTime(renewal.next_attempt_at));
  if (status.acme_staging) result += row('旧测试环境配置', '请取消测试签发并申请正式证书，正式申请成功后才启用自动续签', 'warn');
  if (active && !active.valid) result += row('证书已失效', '服务保留 HTTPS。自动续签会继续尝试；路径或上传证书请及时更新。', 'warn');
  result += row('证书检查范围', 'Meridian 本机证书；浏览器入口的证书与公网连通性需另行确认');
  result += row('当前监听端口', String(status.active_listen_port || '—'));
  result += row('设置监听端口', String(status.listen_port || '—'), status.listen_port !== status.active_listen_port ? 'warn' : '');
  result += row('面板 HTTPS', status.restart_required ? '等待重启应用' : status.tls_enabled ? '已启用' : '未启用', status.restart_required ? 'warn' : 'good');
  return result;
}

async function waitForPanelRestart(redirectURL) {
	const target = new URL(redirectURL, window.location.href);
	Toast.info('服务正在重启，等待新的面板地址就绪…');
	const deadline = Date.now() + 90000;
	while (Date.now() < deadline) {
		try {
			const response = await fetch(new URL('/api/panel-ready', target).href, {
				mode: 'cors', credentials: 'omit', cache: 'no-store', redirect: 'error',
				signal: AbortSignal.timeout(5000),
			});
			if (response.ok && (await response.json()).ready === true) {
				window.location.replace(target.href);
				return;
			}
		} catch (_) {}
		await new Promise(resolve => setTimeout(resolve, 1500));
	}
	Toast.error(`服务重启超时，请检查域名解析与端口，然后打开 ${target.origin}`);
}
