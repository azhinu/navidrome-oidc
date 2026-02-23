const card = document.querySelector('.card');
const nameEl = document.querySelector('[data-name]');
const emailEl = document.querySelector('[data-email]');
const avatarEl = document.querySelector('[data-avatar]');
const errorEl = document.querySelector('[data-error]');
const statusEl = document.querySelector('[data-status]');
const form = document.querySelector('.form');
const passwordInput = form.querySelector('input[name="password"]');
const actionBtn = document.querySelector('[data-action]');
const errorMessagee = "Something went wrong. Please try again later, or ask administrator.";


const state = {
  exists: false,
  goto: null,
  completed: false,
};

document.addEventListener('DOMContentLoaded', () => {
  loadProfile();
});

form.addEventListener('submit', (event) => {
  event.preventDefault();
  if (state.completed && state.goto) {
    window.location.href = state.goto;
    return;
  }
  submitPassword(passwordInput.value);
});

actionBtn.addEventListener('click', (event) => {
  if (state.completed && state.goto) {
    event.preventDefault();
    window.location.href = state.goto;
  }
});

passwordInput.addEventListener('input', () => {
  if (actionBtn.classList.contains('error')) {
    clearSubmitError();
  }
});

async function loadProfile() {
  setLoading(true);
  hideError();
  hideStatus();
  try {
    const res = await fetch('api/me', { credentials: 'include' });
    if (!res.ok) throw new Error('Failed to load profile');
    const data = await res.json();
    state.exists = Boolean(data.exists);
    state.goto = data.nextUrl;
    renderUser(data);
    renderAction();
  } catch (err) {
    showError(errorMessagee);
  } finally {
    setLoading(false);
  }
}

async function submitPassword(password) {
  if (!password) {
    showSubmitError('Password is required.');
    return;
  }
  setLoading(true);
  hideError();
  hideStatus();
  try {
    const res = await fetch('api/password', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ password }),
    });
    let data = null;
    try {
      data = await res.json();
    } catch {
      data = null;
    }
    if (!res.ok) {
      const message = data?.error?.message || errorMessagee;
      throw new Error(message);
    }
    state.completed = true;
    state.goto = data.nextUrl;
    renderSuccess(data.action === 'updated');
  } catch (err) {
    showSubmitError(err?.message || errorMessagee);
  } finally {
    setLoading(false);
  }
}

function renderUser(data) {
  const name = data.name?.trim() || 'User';
  nameEl.textContent = `Hi, ${name}`;
  emailEl.textContent = data.email;
  if (data.picture) {
    avatarEl.style.backgroundImage = `url(${data.picture})`;
    avatarEl.textContent = '';
  } else {
    avatarEl.style.backgroundImage = 'none';
    avatarEl.textContent = name.charAt(0).toUpperCase();
  }
}

function renderAction() {
  state.completed = false;
  actionBtn.classList.remove('success');
  actionBtn.classList.remove('error');
  actionBtn.textContent = state.exists ? 'Update password' : 'Create Navidrome account';
  hideStatus();
  card.setAttribute('data-state', 'ready');
}

function renderSuccess(updated) {
  actionBtn.classList.add('success');
  actionBtn.classList.remove('error');
  actionBtn.textContent = 'Go to Navidrome';
  passwordInput.value = '';
  showStatus(updated ? 'Password changed successfully' : 'Welcome! Your account is ready');
  card.setAttribute('data-state', 'completed');
  if (!updated) {
    state.exists = true;
  }
}

function setLoading(isLoading) {
  if (isLoading) {
    actionBtn.setAttribute('disabled', 'true');
    card.setAttribute('data-state', 'loading');
  } else if (!state.completed) {
    actionBtn.removeAttribute('disabled');
    card.setAttribute('data-state', 'ready');
  } else {
    actionBtn.removeAttribute('disabled');
  }
}

function showError(message) {
  if (!message) return;
  errorEl.textContent = normalizeMessage(message);
  errorEl.hidden = false;
}

function hideError() {
  errorEl.hidden = true;
  errorEl.textContent = '';
}

function showStatus(message) {
  if (!message) return;
  statusEl.textContent = message;
  statusEl.hidden = false;
}

function hideStatus() {
  statusEl.hidden = true;
  statusEl.textContent = '';
}

function showSubmitError(message) {
  const normalized = normalizeMessage(message);
  if (!normalized) return;
  actionBtn.classList.add('error');
  actionBtn.classList.remove('success');
  actionBtn.textContent = normalized;
}

function clearSubmitError() {
  if (state.completed) return;
  actionBtn.classList.remove('error');
  actionBtn.textContent = state.exists ? 'Update password' : 'Create Navidrome account';
}

function normalizeMessage(message) {
  const text = String(message ?? '').trim();
  if (!text) return '';
  const first = text[0];
  const upper = first.toLocaleUpperCase();
  if (upper === first) return text;
  return upper + text.slice(1);
}
