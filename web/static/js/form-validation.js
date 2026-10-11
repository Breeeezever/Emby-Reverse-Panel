// Shared validation for settings and site editors, including inactive tabs.
function parseBoundedInteger(value, min = 0, max = Number.MAX_SAFE_INTEGER) {
  const raw = String(value ?? '').trim();
  if (!/^\d+$/.test(raw)) return null;
  const number = Number(raw);
  return Number.isSafeInteger(number) && number >= min && number <= max ? number : null;
}

function fieldValidationMessage(input) {
  if (input.disabled || input.type === 'hidden' || input.closest('[hidden]')) return '';
  const raw = input.value.trim();
  if (input.type === 'number') {
    const min = input.min === '' ? 0 : Number(input.min);
    const max = input.max === '' ? Number.MAX_SAFE_INTEGER : Number(input.max);
    if ((!raw && (input.required || input.validity.badInput)) ||
        (raw && parseBoundedInteger(raw, min, max) === null)) {
      return input.max === '' ? `请输入不小于 ${min} 的整数` : `请输入 ${min}–${max} 之间的整数`;
    }
  }
  if (input.required && !raw) return '请填写此项';
  if (input.validity.tooLong) return `最多填写 ${input.maxLength} 个字符`;
  if (input.validity.typeMismatch) return input.type === 'email' ? '请输入有效的邮箱地址' : '请检查填写格式';
  return '';
}

let fieldErrorSequence = 0;
function setFieldError(input, message) {
  let error = input._validationError;
  if (!error && message) {
    error = document.createElement('small');
    error.id = `field-error-${++fieldErrorSequence}`;
    error.className = 'field-error';
    error.setAttribute('aria-live', 'polite');
    const settingsField = input.closest('.settings-field, .telegram-field');
    if (settingsField) settingsField.appendChild(error);
    else input.insertAdjacentElement('afterend', error);
    input._validationError = error;
  }
  if (error) {
    error.textContent = message;
    error.hidden = !message;
    const descriptions = new Set((input.getAttribute('aria-describedby') || '').split(/\s+/).filter(Boolean));
    if (message) descriptions.add(error.id);
    else descriptions.delete(error.id);
    if (descriptions.size) input.setAttribute('aria-describedby', [...descriptions].join(' '));
    else input.removeAttribute('aria-describedby');
  }
  if (message) input.setAttribute('aria-invalid', 'true');
  else input.removeAttribute('aria-invalid');
}

function focusInvalidField(input) {
  const panel = input.closest('.site-modal-panel');
  if (panel) document.getElementById(panel.getAttribute('aria-labelledby'))?.click();
  for (let parent = input.parentElement; parent; parent = parent.parentElement) {
    if (parent.tagName === 'DETAILS') parent.open = true;
  }
  input.focus({ preventScroll: true });
  input.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
}

function rejectField(input, message) {
  setFieldError(input, message);
  focusInvalidField(input);
  return false;
}

function validateFields(root) {
  let firstInvalid = null;
  root.querySelectorAll('input, select, textarea').forEach(input => {
    const message = fieldValidationMessage(input);
    setFieldError(input, message);
    if (message && !firstInvalid) firstInvalid = input;
  });
  if (firstInvalid) focusInvalidField(firstInvalid);
  return !firstInvalid;
}

function bindFieldValidation(root) {
  if (root._fieldValidationBound) return;
  root._fieldValidationBound = true;
  const validateInput = event => {
    const input = event.target;
    if (!input.matches('input, select, textarea')) return;
    if (input.type === 'number' || input.hasAttribute('aria-invalid')) {
      setFieldError(input, fieldValidationMessage(input));
    }
  };
  root.addEventListener('input', validateInput);
  root.addEventListener('change', validateInput);
  root.addEventListener('focusout', validateInput);
}
