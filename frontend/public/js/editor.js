// State management
const state = {
    wsUrl: null, // WebSocket URL
    ws: null, // WebSocket instance
    clientId: null, // Our client ID
    documentId: null, // Current document ID
    documentVersion: 0,
    activeUsers: new Map(), // Map of active users
    typingUsers: new Set(), // Set of users currently typing
    isUpdatingFromRemote: false, // Flag to prevent echoing updates
    reconnectAttempts: 0, // Reconnection attempts
    maxReconnectAttempts: 10, // Max reconnection attempts
    reconnectDelay: 2000, // Initial delay for reconnection
    remoteCursors: new Map(), // ADD THIS
    remoteSelections: new Map(), // ADD THIS
};

// UI Elements
const elements = {
    editor: null, // Textarea element
    statusDot: null, // Status dot element
    statusText: null, // Status text element
    docId: null, // Document ID element
    clientId: null, // Client ID element
    activeUsers: null, // Active users element
    userCount: null, // User count element
    typingIndicators: null, // Typing indicators element
    notification: null, // Notification element
    lastSaved: null // Last saved element
};

// User colors palette
const colors = ['#FF6B6B', '#4ECDC4', '#45B7D1', '#96CEB4', '#FFEAA7', '#DDA0DD', '#98D8C8', '#FFA07A'];

// Initialize elements after DOM loads
function initElements() {
    elements.editor = document.getElementById('editor');
    elements.statusDot = document.getElementById('statusDot');
    elements.statusText = document.getElementById('statusText');
    elements.docId = document.getElementById('docId');
    elements.clientId = document.getElementById('clientId');
    elements.activeUsers = document.getElementById('activeUsers');
    elements.userCount = document.getElementById('userCount');
    elements.typingIndicators = document.getElementById('typingIndicators');
    elements.notification = document.getElementById('notification');
    elements.lastSaved = document.getElementById('lastSaved');
}

// Initialize application
function init() {
    initElements();

    const urlParams = new URLSearchParams(window.location.search);
    state.documentId = urlParams.get('doc') || 'default-doc';
    elements.docId.textContent = state.documentId;

    // Start with correct initial count
    elements.userCount.textContent = '1 user online';

    state.wsUrl = `ws://localhost:8080/ws?doc=${state.documentId}`;
    console.log('Initializing with URL:', state.wsUrl);

    connect();
    setupEventListeners();
}

// WebSocket connection
function connect() {
    updateConnectionStatus('reconnecting', `Connecting... (Attempt ${state.reconnectAttempts + 1})`);

    state.ws = new WebSocket(state.wsUrl);

    state.ws.onopen = handleWebSocketOpen; // Handle connection open
    state.ws.onmessage = handleWebSocketMessage; // Handle incoming messages
    state.ws.onerror = handleWebSocketError; // Handle errors
    state.ws.onclose = handleWebSocketClose; // Handle connection close
}

// WebSocket event handlers
function handleWebSocketOpen() {
    console.log('WebSocket connected');
    state.reconnectAttempts = 0;
    updateConnectionStatus('connected', 'Connected');
    requestDocumentState();
}

function handleWebSocketMessage(event) {
    const messages = event.data.split('\n').filter(m => m.trim()); // Handle multiple messages

    messages.forEach(msgString => {
        try {
            const msg = JSON.parse(msgString);
            console.log('Parsed message:', msg.type, msg);
            handleMessage(msg);
        } catch (e) {
            console.error('Error parsing single message:', msgString, e);
        }
    });
}

function handleWebSocketError(error) {
    console.error('WebSocket error:', error);
    updateConnectionStatus('disconnected', 'Connection error');
}

function handleWebSocketClose() {
    console.log('WebSocket disconnected');
    updateConnectionStatus('disconnected', 'Disconnected');
    handleReconnect();
}

// Handle reconnection
function handleReconnect() {
    if (state.reconnectAttempts < state.maxReconnectAttempts) {
        state.reconnectAttempts++;
        const delay = Math.min(state.reconnectDelay * state.reconnectAttempts, 30000);
        updateConnectionStatus('reconnecting', `Reconnecting in ${delay / 1000}s...`);
        setTimeout(connect, delay);
    } else {
        updateConnectionStatus('disconnected', 'Connection failed');
    }
}

// Update connection status UI
function updateConnectionStatus(status, text) {
    elements.statusDot.className = `status-dot ${status}`;
    elements.statusText.textContent = text;
}

// Handle incoming messages
function handleMessage(msg) {
    console.log('Received:', msg);

    switch (msg.type) {
        case 'init':
            handleInit(msg);
            break;
        case 'document_state':
            handleDocumentState(msg);
            break;
        case 'text_update':
            handleTextUpdate(msg);
            break;
        case 'user_joined':
            handleUserJoined(msg);
            break;
        case 'user_left':
            handleUserLeft(msg);
            break;
        case 'active_users':
            updateActiveUsers(msg.data || []);
            break;
        case 'typing_start':
            handleTypingStart(msg);
            break;
        case 'typing_stop':
            handleTypingStop(msg);
            break;
        case 'save_confirmation':
            handleSaveConfirmation(msg);
            break;
        case 'cursor_position':
            handleCursorPosition(msg);
            break;
        case 'selection_change':
            handleSelectionChange(msg);
            break;
        case 'cursor_remove':
            handleCursorRemove(msg);
                break;
        default:
            console.warn('Unknown message type:', msg.type);
    }
}

// Message handlers
function handleInit(msg) {
    state.clientId = msg.clientId;
    elements.clientId.textContent = msg.clientId || '...';
}

function handleDocumentState(msg) {
    console.log('Document state received, version:', msg.version);
    state.isUpdatingFromRemote = true;
    elements.editor.value = msg.content || '';
    state.documentVersion = msg.version || 0;
    state.isUpdatingFromRemote = false;
}

function handleTextUpdate(msg) {
    console.log('=== TEXT UPDATE RECEIVED ===');
    console.log('From:', msg.clientId);
    console.log('My ID:', state.clientId);
    console.log('Version:', msg.version);
    console.log('Content length:', msg.content?.length);

    if (msg.clientId !== state.clientId) {
        console.log('Applying remote update');
        state.isUpdatingFromRemote = true;
        elements.editor.value = msg.content;
        // Update local version to match server
        state.documentVersion = msg.version || state.documentVersion + 1;
        state.isUpdatingFromRemote = false;
        console.log('Update applied, new version:', state.documentVersion);
    } else {
        // Even for our own updates, update the version
        state.documentVersion = msg.version || state.documentVersion + 1;
        console.log('Own update acknowledged, version:', state.documentVersion);
    }
}

function handleUserJoined(msg) {
    const userId = msg.clientId || msg.userId;

    // Don't add ourselves
    if (userId === state.clientId) return;

    const username = msg.data?.username || msg.username || `User-${userId?.substring(0, 4)}`;
    const color = msg.data?.color || msg.color || colors[state.activeUsers.size % colors.length];

    // Only add if not already present
    if (!state.activeUsers.has(userId)) {
        state.activeUsers.set(userId, { username, color });
        updateUsersUI();
        showNotification(`${username} joined`, 'join');
    }
}

function handleUserLeft(msg) {
    const userId = msg.clientId || msg.userId;
    const user = state.activeUsers.get(userId);
    if (user) {
        showNotification(`${user.username} left`, 'leave');
        state.activeUsers.delete(userId);
        state.typingUsers.delete(userId);
        updateUsersUI();
        updateTypingIndicators();
    }
}

function handleTypingStart(msg) {
    const userId = msg.clientId || msg.userId;
    if (userId !== state.clientId) {
        state.typingUsers.add(userId);
        updateUsersUI();
        updateTypingIndicators();
    }
}

function handleTypingStop(msg) {
    const userId = msg.clientId || msg.userId;
    state.typingUsers.delete(userId);
    updateUsersUI();
    updateTypingIndicators();
}

function handleSaveConfirmation(msg) {
    elements.lastSaved.textContent = `Saved at ${new Date().toLocaleTimeString()}`;
    showNotification('Document saved', 'info');
}

// Update active users list
function updateActiveUsers(users) {
    console.log('=== updateActiveUsers called ===');
    console.log('Received users array:', users);
    console.log('My clientId:', state.clientId);

    state.activeUsers.clear();

    let selfFound = false;
    users.forEach(user => {
        console.log('Processing user:', user);

        if (user.userId === state.clientId) {
            selfFound = true;
            console.log('Found self in list');
        } else {
            state.activeUsers.set(user.userId, {
                username: user.username || `User-${user.userId?.substring(0, 4)}`,
                color: user.color || colors[state.activeUsers.size % colors.length]
            });
            console.log('Added other user:', user.userId);
        }
    });

    console.log('Final activeUsers map:', state.activeUsers);
    console.log('Self found in list:', selfFound);

    updateUsersUI();
}

// Update users UI
function updateUsersUI() {
    const count = state.activeUsers.size + 1; // +1 for self

    console.log('=== updateUsersUI ===');
    console.log('activeUsers.size:', state.activeUsers.size);
    console.log('Total count:', count);

    elements.userCount.textContent = `${count} ${count === 1 ? 'user' : 'users'} online`;

    // Clear and rebuild avatars
    elements.activeUsers.innerHTML = '';

    // Add self first
    if (state.clientId) {
        const selfAvatar = createUserAvatar('ME', '#6c757d', 'You', true);
        elements.activeUsers.appendChild(selfAvatar);
    }

    // Add other users
    state.activeUsers.forEach((user, userId) => {
        const avatar = createUserAvatar(
            user.username.substring(0, 2).toUpperCase(),
            user.color,
            user.username,
            false,
            userId
        );

        if (state.typingUsers.has(userId)) {
            avatar.classList.add('typing');
        }

        elements.activeUsers.appendChild(avatar);
    });

    console.log(`UI Updated: ${count} total users (${state.activeUsers.size} others + self)`);
}

// Create user avatar element
function createUserAvatar(text, color, title, isSelf = false, userId = null) {
    const avatar = document.createElement('div');
    avatar.className = 'user-avatar';
    avatar.style.background = color;
    avatar.textContent = text;
    avatar.title = title;

    if (isSelf) {
        avatar.style.border = '2px solid white';
    }

    if (userId) {
        avatar.dataset.userId = userId;
    }

    return avatar;
}

// Update typing indicators
function updateTypingIndicators() {
    elements.typingIndicators.innerHTML = '';

    state.typingUsers.forEach(userId => {
        const user = state.activeUsers.get(userId);
        if (user) {
            const indicator = createTypingIndicator(user.username, user.color);
            elements.typingIndicators.appendChild(indicator);
        }
    });
}

// Create typing indicator element
function createTypingIndicator(username, color) {
    const indicator = document.createElement('div');
    indicator.className = 'typing-user';
    indicator.innerHTML = `
        <span style="color: ${color}">${username}</span>
        <span>is typing</span>
        <div class="typing-dots">
            <div class="typing-dot"></div>
            <div class="typing-dot"></div>
            <div class="typing-dot"></div>
        </div>
    `;
    return indicator;
}

// Show notification
function showNotification(message, type = 'info') {
    elements.notification.textContent = message;
    elements.notification.className = `notification ${type} show`;

    setTimeout(() => {
        elements.notification.classList.remove('show');
    }, 3000);
}

// Setup event listeners
function setupEventListeners() {
    let typingTimer;
    let isTyping = false;

    elements.editor.addEventListener('input', () => {
        if (state.isUpdatingFromRemote) return;

        // This should still be here for visual feedback
        if (!isTyping) {
            isTyping = true;
            sendMessage({
                type: 'typing_start',
                clientId: state.clientId
            });
        }

        clearTimeout(typingTimer);
        typingTimer = setTimeout(() => {
            // Send text with OT version
            sendMessage({
                type: 'text_update',
                content: elements.editor.value,
                clientId: state.clientId,
                documentId: state.documentId,
                version: state.documentVersion  // OT addition
            });

            // Still send typing stop for visual feedback
            if (isTyping) {
                isTyping = false;
                sendMessage({
                    type: 'typing_stop',
                    clientId: state.clientId
                });
            }
        }, 500);
    });
    trackCursorPosition();
    console.log('Cursor tracking initialized');
}

function trackCursorPosition() {
    const editor = elements.editor;

    editor.addEventListener('click', () => {
        console.log('Click detected, sending cursor position');
        updateCursorPosition();
    });

    editor.addEventListener('keyup', () => {
        updateCursorPosition();
    });

    editor.addEventListener('select', () => {
        updateSelection();
    });

    editor.addEventListener('mouseup', () => {
        updateSelection();
    });
}

function updateCursorPosition() {
    const position = elements.editor.selectionStart;

    sendMessage({
        type: 'cursor_position',
        clientId: state.clientId,
        documentId: state.documentId,
        position: position
    });
}

function updateSelection() {
    const start = elements.editor.selectionStart;
    const end = elements.editor.selectionEnd;

    sendMessage({
        type: 'selection_change',
        clientId: state.clientId,
        documentId: state.documentId,
        data: {  // Changed from 'selection' to 'data'
            start: start,
            end: end
        }
    });
}

// Send message to server
function sendMessage(msg) {
    if (state.ws && state.ws.readyState === WebSocket.OPEN) {
        state.ws.send(JSON.stringify(msg));
        console.log('Sent:', msg);
    }
}

// Request document state
function requestDocumentState() {
    sendMessage({
        type: 'request_document',
        documentId: state.documentId
    });
}

// Save document
function saveDocument() {
    sendMessage({
        type: 'save_document',
        documentId: state.documentId,
        content: elements.editor.value
    });
}

// Better position calculation using a mirror div
function getPositionFromIndex(textarea, index) {
    // Create invisible div with same styling as textarea
    const mirror = document.createElement('div');
    const computed = window.getComputedStyle(textarea);

    // Copy styling
    mirror.style.position = 'absolute';
    mirror.style.visibility = 'hidden';
    mirror.style.whiteSpace = 'pre-wrap';
    mirror.style.wordWrap = 'break-word';
    mirror.style.font = computed.font;
    mirror.style.padding = computed.padding;
    mirror.style.width = computed.width;

    // Copy text up to cursor position
    mirror.textContent = textarea.value.substring(0, index);
    document.body.appendChild(mirror);

    // Add a span at the cursor position
    const span = document.createElement('span');
    span.textContent = '|';
    mirror.appendChild(span);

    // Get position
    const position = {
        x: span.offsetLeft,
        y: span.offsetTop
    };

    // Clean up
    document.body.removeChild(mirror);

    return position;
}

function displayRemoteCursors() {
    const editor = elements.editor;

    state.remoteCursors.forEach((cursor, clientId) => {
        let cursorEl = document.getElementById(`cursor-${clientId}`);

        if (!cursorEl) {
            // Create cursor element
            cursorEl = document.createElement('div');
            cursorEl.id = `cursor-${clientId}`;
            cursorEl.className = 'remote-cursor';
            cursorEl.style.color = cursor.color;
            cursorEl.setAttribute('data-username', cursor.username);

            // Make sure editor has a wrapper for positioning
            if (!editor.parentElement.classList.contains('editor-wrapper')) {
                editor.parentElement.classList.add('editor-wrapper');
            }
            editor.parentElement.appendChild(cursorEl);
        }

        // Use the helper function here
        const position = getPositionFromIndex(editor, cursor.position);
        if (position) {
            cursorEl.style.left = `${position.x}px`;
            cursorEl.style.top = `${position.y}px`;
        }
    });
}

// Handle cursor position message
function handleCursorPosition(msg) {
    const data = msg.data;
    if (data.clientId === state.clientId) return; // Ignore own cursor

    console.log('Remote cursor update:', data);

    // Store cursor position
    state.remoteCursors.set(data.clientId, {
        position: data.position,
        username: data.username,
        color: data.color
    });

    // Update display
    displayRemoteCursors();
}

// Handle selection change message
function handleSelectionChange(msg) {
    const data = msg.data;
    if (data.clientId === state.clientId) return;

    console.log('Remote selection update:', data);

    // Store selection
    if (data.start === data.end) {
        state.remoteSelections.delete(data.clientId);
        // Remove the visual element immediately
        const selectionEl = document.getElementById(`selection-${data.clientId}`);
        if (selectionEl) {
            selectionEl.remove();
        }
    } else {
        state.remoteSelections.set(data.clientId, {
            start: data.start,
            end: data.end,
            username: data.username,
            color: data.color
        });
    }

    // Update display
    displayRemoteSelections();
}

// Handle cursor remove message
function handleCursorRemove(msg) {
    const clientId = msg.data.clientId;

    // Remove cursor and selection
    state.remoteCursors.delete(clientId);
    state.remoteSelections.delete(clientId);

    // Remove from display
    const cursorEl = document.getElementById(`cursor-${clientId}`);
    if (cursorEl) cursorEl.remove();

    const selectionEl = document.getElementById(`selection-${clientId}`);
    if (selectionEl) selectionEl.remove();
}

// Display remote selections
function displayRemoteSelections() {
    const editor = elements.editor;

    state.remoteSelections.forEach((selection, clientId) => {
        let selectionEl = document.getElementById(`selection-${clientId}`);

        if (!selectionEl) {
            selectionEl = document.createElement('div');
            selectionEl.id = `selection-${clientId}`;
            selectionEl.className = 'remote-selection';
            selectionEl.style.backgroundColor = selection.color;

            // Add to editor container, not parent
            const container = editor.parentElement;
            container.style.position = 'relative';
            container.appendChild(selectionEl);
        }

        const startPos = getPositionFromIndex(editor, selection.start);
        const endPos = getPositionFromIndex(editor, selection.end);

        if (startPos && endPos) {
            // For single line selection
            if (startPos.y === endPos.y) {
                selectionEl.style.left = `${startPos.x}px`;
                selectionEl.style.top = `${startPos.y}px`;
                selectionEl.style.width = `${endPos.x - startPos.x}px`;
                selectionEl.style.height = '22px';
            } else {
                // For multi-line, just highlight from start to end of first line for now
                selectionEl.style.left = `${startPos.x}px`;
                selectionEl.style.top = `${startPos.y}px`;
                selectionEl.style.width = '100px'; // Simplified
                selectionEl.style.height = `${endPos.y - startPos.y + 22}px`;
            }
        }
    });
}

// Initialize on DOM load
window.addEventListener('DOMContentLoaded', init);