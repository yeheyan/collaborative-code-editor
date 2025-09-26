// Collaborative Editor JavaScript

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
    reconnectDelay: 2000 // Initial delay for reconnection
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

// Initialize on DOM load
window.addEventListener('DOMContentLoaded', init);