const card = document.querySelector('.card');
const nameEl = document.querySelector('[data-name]');
const emailEl = document.querySelector('[data-email]');
const avatarEl = document.querySelector('[data-avatar]');
const statusEl = document.querySelector('[data-status]');
const form = document.querySelector('.form');
const passwordInput = form.querySelector('input[name="password"]');
const actionBtn = document.querySelector('[data-action]');

const defaultErrorMessage = 'Something went wrong. Try again later.';

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
  hideStatus();
  clearSubmitError();
  try {
    const res = await fetch('api/me', { credentials: 'include' });
    if (!res.ok) throw new Error(defaultErrorMessage);
    const data = await res.json();
    state.exists = Boolean(data.exists);
    state.goto = data.nextUrl;
    renderUser(data);
    renderAction();
  } catch (err) {
    showSubmitError(err?.message || defaultErrorMessage);
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
  hideStatus();
  clearSubmitError();
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
      const message = data?.error?.message || defaultErrorMessage;
      throw new Error(message);
    }

    state.completed = true;
    state.goto = data.nextUrl;
    renderSuccess(data.action === 'updated');
  } catch (err) {
    showSubmitError(err?.message || defaultErrorMessage);
  } finally {
    setLoading(false);
  }
}

function renderUser(data) {
  const name = data.name?.trim() || 'User';
  nameEl.textContent = `Hi, ${name}`;
  emailEl.textContent = data.email;
  if (data.picture) {
    avatarEl.setAttribute('data-has-image', 'true');
    avatarEl.style.backgroundImage = `url(${data.picture})`;
    avatarEl.textContent = '';
  } else {
    avatarEl.removeAttribute('data-has-image');
    avatarEl.style.backgroundImage = 'none';
    avatarEl.textContent = name.charAt(0).toUpperCase();
  }
}

function renderAction() {
  state.completed = false;
  actionBtn.classList.remove('error');
  actionBtn.textContent = state.exists ? 'Change password' : 'Sign-up';
  card.setAttribute('data-state', 'ready');
}

function renderSuccess(updated) {
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
    if (!state.completed) {
      actionBtn.textContent = 'Loading…';
    }
    return;
  }

  actionBtn.removeAttribute('disabled');
  if (!state.completed) {
    card.setAttribute('data-state', 'ready');
  }
}

function showStatus(message) {
  const text = String(message ?? '').trim();
  if (!text) return;
  statusEl.textContent = text;
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
  actionBtn.textContent = normalized;
}

function clearSubmitError() {
  if (state.completed) return;
  actionBtn.classList.remove('error');
  actionBtn.textContent = state.exists ? 'Change password' : 'Sign-up';
}

function normalizeMessage(message) {
  const text = String(message ?? '').trim();
  if (!text) return '';
  const first = text[0];
  const upper = first.toLocaleUpperCase();
  if (upper === first) return text;
  return upper + text.slice(1);
}
