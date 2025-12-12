package main

import (
	"github.com/gin-gonic/gin"
)

// handleDashboard 处理管理员仪表板（完整版）
func handleDashboard() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Data(200, "text/html; charset=utf-8", []byte(DashboardHTML))
	}
}

// DashboardHTML 完整的仪表板 HTML
const DashboardHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>LLM Gateway Admin Dashboard</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.4.0/css/all.min.css">
    <style>
        .modal-backdrop {
            backdrop-filter: blur(4px);
            background-color: rgba(0, 0, 0, 0.5);
        }
        .key-hidden {
            filter: blur(4px);
            transition: filter 0.3s ease;
        }
        .key-hidden:hover {
            filter: none;
        }
        .fade-in {
            animation: fadeIn 0.3s ease-in;
        }
        @keyframes fadeIn {
            from { opacity: 0; transform: translateY(-10px); }
            to { opacity: 1; transform: translateY(0); }
        }
        .loading {
            display: inline-block;
            width: 20px;
            height: 20px;
            border: 3px solid rgba(255,255,255,.3);
            border-radius: 50%;
            border-top-color: #fff;
            animation: spin 1s ease-in-out infinite;
        }
        @keyframes spin {
            to { transform: rotate(360deg); }
        }
    </style>
</head>
<body class="bg-gray-100 min-h-screen">
    <!-- Login Modal -->
    <div id="loginModal" class="fixed inset-0 z-50 flex items-center justify-center modal-backdrop">
        <div class="bg-white rounded-lg shadow-xl w-full max-w-md mx-4 fade-in">
            <div class="px-6 py-4 border-b">
                <h3 class="text-xl font-bold flex items-center gap-2">
                    <i class="fas fa-shield-alt text-blue-600"></i>
                    Admin Authentication
                </h3>
            </div>
            <form id="loginForm" class="p-6">
                <div class="mb-4">
                    <label class="block text-sm font-medium text-gray-700 mb-2">Admin API Key</label>
                    <input type="password" id="adminKeyInput" required
                           class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none"
                           placeholder="Enter your admin API key">
                </div>
                <button type="submit" class="w-full bg-blue-600 text-white rounded-lg py-2 hover:bg-blue-700 transition flex items-center justify-center gap-2">
                    <i class="fas fa-sign-in-alt"></i>
                    <span id="loginButtonText">Login</span>
                </button>
            </form>
        </div>
    </div>

    <!-- Header -->
    <header class="bg-gradient-to-r from-blue-600 to-purple-600 text-white shadow-lg">
        <div class="container mx-auto px-6 py-6">
            <div class="flex items-center justify-between">
                <div>
                    <h1 class="text-3xl font-bold flex items-center gap-3">
                        <i class="fas fa-rocket"></i>
                        LLM Gateway Admin Dashboard
                    </h1>
                    <p class="mt-2 text-blue-100">Manage your model groups and API keys</p>
                </div>
                <div class="flex items-center gap-4">
                    <button onclick="refreshDashboard()" id="refreshBtn" class="bg-white/20 hover:bg-white/30 px-4 py-2 rounded-lg transition flex items-center gap-2">
                        <i class="fas fa-sync-alt"></i>
                        Refresh
                    </button>
                    <button onclick="showAddGroupModal()" class="bg-green-500 hover:bg-green-600 px-4 py-2 rounded-lg transition flex items-center gap-2">
                        <i class="fas fa-plus"></i>
                        Add Group
                    </button>
                    <button onclick="logout()" class="bg-red-500 hover:bg-red-600 px-4 py-2 rounded-lg transition flex items-center gap-2">
                        <i class="fas fa-sign-out-alt"></i>
                        Logout
                    </button>
                </div>
            </div>
        </div>
    </header>

    <!-- Main Content -->
    <main class="container mx-auto px-6 py-8">
        <!-- Stats Overview -->
        <div class="grid grid-cols-1 md:grid-cols-4 gap-6 mb-8">
            <div class="bg-white rounded-lg shadow p-6">
                <div class="flex items-center">
                    <div class="p-3 bg-blue-100 rounded-lg">
                        <i class="fas fa-layer-group text-blue-600 text-xl"></i>
                    </div>
                    <div class="ml-4">
                        <p class="text-sm text-gray-500">Total Groups</p>
                        <p class="text-2xl font-bold text-gray-800" id="totalGroups">0</p>
                    </div>
                </div>
            </div>
            <div class="bg-white rounded-lg shadow p-6">
                <div class="flex items-center">
                    <div class="p-3 bg-green-100 rounded-lg">
                        <i class="fas fa-server text-green-600 text-xl"></i>
                    </div>
                    <div class="ml-4">
                        <p class="text-sm text-gray-500">Total Models</p>
                        <p class="text-2xl font-bold text-gray-800" id="totalModels">0</p>
                    </div>
                </div>
            </div>
            <div class="bg-white rounded-lg shadow p-6">
                <div class="flex items-center">
                    <div class="p-3 bg-yellow-100 rounded-lg">
                        <i class="fas fa-key text-yellow-600 text-xl"></i>
                    </div>
                    <div class="ml-4">
                        <p class="text-sm text-gray-500">API Keys</p>
                        <p class="text-2xl font-bold text-gray-800" id="totalKeys">0</p>
                    </div>
                </div>
            </div>
            <div class="bg-white rounded-lg shadow p-6">
                <div class="flex items-center">
                    <div class="p-3 bg-purple-100 rounded-lg">
                        <i class="fas fa-chart-line text-purple-600 text-xl"></i>
                    </div>
                    <div class="ml-4">
                        <p class="text-sm text-gray-500">Total Requests</p>
                        <p class="text-2xl font-bold text-gray-800" id="totalRequests">0</p>
                    </div>
                </div>
            </div>
        </div>

        <!-- Loading indicator -->
        <div id="loadingIndicator" class="hidden text-center py-12">
            <i class="fas fa-spinner fa-spin text-4xl text-blue-600"></i>
            <p class="mt-4 text-gray-600">Loading data...</p>
        </div>

        <!-- Groups Container -->
        <div id="groupsContainer" class="space-y-6">
            <!-- Groups will be dynamically loaded here -->
        </div>
    </main>

    <!-- Add Group Modal -->
    <div id="addGroupModal" class="hidden fixed inset-0 z-50 flex items-center justify-center modal-backdrop">
        <div class="bg-white rounded-lg shadow-xl w-full max-w-2xl mx-4 fade-in">
            <div class="px-6 py-4 border-b">
                <h3 class="text-xl font-bold">Add Model Group</h3>
            </div>
            <form id="addGroupForm" class="p-6">
                <div class="space-y-4">
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-2">Group ID</label>
                        <input type="text" name="group_id" required
                               class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none"
                               placeholder="e.g., ai-free-pool">
                    </div>
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-2">Strategy</label>
                        <select name="strategy" class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none">
                            <option value="fallback">Fallback (Failover)</option>
                            <option value="round_robin">Round Robin</option>
                        </select>
                    </div>
                </div>
                <div class="mt-6 flex justify-end gap-3">
                    <button type="button" onclick="closeModal('addGroupModal')" class="px-4 py-2 text-gray-600 hover:text-gray-800">Cancel</button>
                    <button type="submit" class="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700">Add Group</button>
                </div>
            </form>
        </div>
    </div>

    <!-- Add Model Modal -->
    <div id="addModelModal" class="hidden fixed inset-0 z-50 flex items-center justify-center modal-backdrop">
        <div class="bg-white rounded-lg shadow-xl w-full max-w-2xl mx-4 fade-in">
            <div class="px-6 py-4 border-b">
                <h3 class="text-xl font-bold">Add Model</h3>
            </div>
            <form id="addModelForm" class="p-6">
                <input type="hidden" id="addModelGroupId" name="group_id">
                <div class="space-y-4">
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-2">Provider</label>
                        <input type="text" name="provider_name" required
                               class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none"
                               placeholder="e.g., openai, anthropic, google">
                    </div>
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-2">Upstream URL</label>
                        <input type="url" name="upstream_url" required
                               class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none"
                               placeholder="e.g., https://api.openai.com/v1">
                    </div>
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-2">Model Name</label>
                        <input type="text" name="upstream_model" required
                               class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none"
                               placeholder="e.g., gpt-3.5-turbo">
                    </div>
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-2">Timeout (seconds)</label>
                        <input type="number" name="timeout" value="30" min="1" max="300"
                               class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none">
                    </div>
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-2">API Keys (one per line)</label>
                        <textarea name="keys" rows="3" required
                                  class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none"
                                  placeholder="sk-...\nsk-..."></textarea>
                    </div>
                </div>
                <div class="mt-6 flex justify-end gap-3">
                    <button type="button" onclick="closeModal('addModelModal')" class="px-4 py-2 text-gray-600 hover:text-gray-800">Cancel</button>
                    <button type="submit" class="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700">Add Model</button>
                </div>
            </form>
        </div>
    </div>

    <!-- Add Key Modal -->
    <div id="addKeyModal" class="hidden fixed inset-0 z-50 flex items-center justify-center modal-backdrop">
        <div class="bg-white rounded-lg shadow-xl w-full max-w-md mx-4 fade-in">
            <div class="px-6 py-4 border-b">
                <h3 class="text-xl font-bold">Add API Key</h3>
            </div>
            <form id="addKeyForm" class="p-6">
                <input type="hidden" id="addKeyModelId" name="model_id">
                <div class="mb-4">
                    <label class="block text-sm font-medium text-gray-700 mb-2">API Key</label>
                    <input type="password" name="key" required
                           class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none"
                           placeholder="sk-...">
                </div>
                <div class="flex justify-end gap-3">
                    <button type="button" onclick="closeModal('addKeyModal')" class="px-4 py-2 text-gray-600 hover:text-gray-800">Cancel</button>
                    <button type="submit" class="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700">Add Key</button>
                </div>
            </form>
        </div>
    </div>

    <!-- Edit Model Modal -->
    <div id="editModelModal" class="hidden fixed inset-0 z-50 flex items-center justify-center modal-backdrop">
        <div class="bg-white rounded-lg shadow-xl w-full max-w-2xl mx-4 fade-in">
            <div class="px-6 py-4 border-b">
                <h3 class="text-xl font-bold">Edit Model</h3>
            </div>
            <form id="editModelForm" class="p-6">
                <input type="hidden" id="editModelId" name="id">
                <div class="space-y-4">
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-2">Provider</label>
                        <input type="text" id="editProviderName" name="provider_name" required
                               class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none"
                               placeholder="e.g., openai, anthropic, google">
                    </div>
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-2">Upstream URL</label>
                        <input type="url" id="editUpstreamUrl" name="upstream_url" required
                               class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none"
                               placeholder="e.g., https://api.openai.com/v1">
                    </div>
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-2">Model Name</label>
                        <input type="text" id="editUpstreamModel" name="upstream_model" required
                               class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none"
                               placeholder="e.g., gpt-3.5-turbo">
                    </div>
                    <div>
                        <label class="block text-sm font-medium text-gray-700 mb-2">Timeout (seconds)</label>
                        <input type="number" id="editTimeout" name="timeout" min="1" max="300"
                               class="w-full px-3 py-2 border rounded-lg focus:ring-2 focus:ring-blue-500 focus:outline-none">
                    </div>
                </div>
                <div class="mt-6 flex justify-end gap-3">
                    <button type="button" onclick="closeModal('editModelModal')" class="px-4 py-2 text-gray-600 hover:text-gray-800">Cancel</button>
                    <button type="submit" class="px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700">Save Changes</button>
                </div>
            </form>
        </div>
    </div>

    <!-- Toast Notification -->
    <div id="toast" class="hidden fixed bottom-4 right-4 bg-white rounded-lg shadow-lg p-4 flex items-center gap-3 fade-in">
        <i id="toastIcon" class="fas fa-check-circle text-green-500 text-xl"></i>
        <span id="toastMessage">Success</span>
    </div>

    <script>
        // Global state
        let adminKey = localStorage.getItem('admin_key');
        let dashboardData = [];

        // Initialize dashboard
        document.addEventListener('DOMContentLoaded', function() {
            console.log('Dashboard initializing...');

            if (adminKey) {
                console.log('Found existing admin key in localStorage');
                document.getElementById('loginModal').classList.add('hidden');
                loadDashboard();
            } else {
                console.log('No admin key found, showing login modal');
                document.getElementById('loginModal').classList.remove('hidden');
            }

            // Setup form listeners
            document.getElementById('loginForm').addEventListener('submit', handleLogin);
            document.getElementById('addGroupForm').addEventListener('submit', handleAddGroup);
            document.getElementById('addModelForm').addEventListener('submit', handleAddModel);
            document.getElementById('addKeyForm').addEventListener('submit', handleAddKey);
            document.getElementById('editModelForm').addEventListener('submit', handleEditModel);
        });

        // API helper function with debug logging
        async function fetchAPI(url, options = {}) {
            const defaultOptions = {
                headers: {
                    'Content-Type': 'application/json',
                    'Authorization': 'Bearer ' + adminKey
                }
            };

            const finalOptions = { ...defaultOptions, ...options };

            // Debug logging
            console.log('=== API Request Debug ===');
            console.log('URL:', url);
            console.log('Method:', finalOptions.method || 'GET');
            console.log('Headers:', finalOptions.headers);
            console.log('Body:', finalOptions.body);
            console.log('========================');

            const response = await fetch(url, finalOptions);

            console.log('=== API Response Debug ===');
            console.log('Status:', response.status);
            console.log('Status Text:', response.statusText);

            if (response.status === 401) {
                console.log('401 Unauthorized - showing error instead of immediate logout');
                // 不要立即刷新页面，而是显示错误消息
                const errorData = await response.json().catch(() => ({}));
                throw new Error(errorData?.error?.message || 'API Key 无效或过期，请重新输入');
            }

            const data = await response.json();
            console.log('Response Data:', data);
            console.log('==========================');

            return { response, data };
        }

        // Authentication functions
        async function handleLogin(e) {
            e.preventDefault();
            const key = document.getElementById('adminKeyInput').value.trim();
            const loginBtn = e.target.querySelector('button[type="submit"]');
            const loginText = document.getElementById('loginButtonText');

            if (!key) {
                showToast('Please enter an API key', 'error');
                return;
            }

            adminKey = key;
            localStorage.setItem('admin_key', key);

            loginBtn.disabled = true;
            loginText.innerHTML = '<span class="loading"></span> Authenticating...';

            try {
                const result = await fetchAPI('/admin/model-groups');

                if (result.response.ok) {
                    console.log('Authentication successful');
                    document.getElementById('loginModal').classList.add('hidden');
                    loadDashboard();
                } else {
                    console.error('Authentication failed:', result.data);
                    showToast(result?.data?.error?.message || 'Invalid API key', 'error');
                    // 清除无效的 key
                    localStorage.removeItem('admin_key');
                    adminKey = null;
                }
            } catch (error) {
                console.error('Authentication error:', error);
                showToast(error.message || 'Authentication failed', 'error');
                // 清除无效的 key
                localStorage.removeItem('admin_key');
                adminKey = null;
            } finally {
                loginBtn.disabled = false;
                loginText.textContent = 'Login';
            }
        }

        function logout() {
            console.log('Logging out...');
            localStorage.removeItem('admin_key');
            adminKey = null;
            location.reload();
        }

        // Load dashboard data
        async function loadDashboard() {
            document.getElementById('loadingIndicator').classList.remove('hidden');
            document.getElementById('refreshBtn').disabled = true;
            document.getElementById('refreshBtn').innerHTML = '<span class="loading"></span> Loading...';

            try {
                const result = await fetchAPI('/admin/model-groups');
                if (result && result.response.ok) {
                    dashboardData = result.data.data || [];
                    console.log('Dashboard data loaded:', dashboardData.length, 'groups');
                    await loadDetailedData();
                    renderDashboard();
                    updateStats();
                } else {
                    showToast(result?.data?.error?.message || 'Failed to load data', 'error');
                }
            } catch (error) {
                console.error('Load dashboard error:', error);
                showToast(error.message || 'Failed to load dashboard data', 'error');

                // 如果是认证错误，显示登录框
                if (error.message.includes('API Key') || error.message.includes('认证')) {
                    document.getElementById('loginModal').classList.remove('hidden');
                }
            } finally {
                document.getElementById('loadingIndicator').classList.add('hidden');
                document.getElementById('refreshBtn').disabled = false;
                document.getElementById('refreshBtn').innerHTML = '<i class="fas fa-sync-alt"></i> Refresh';
            }
        }

        // Load detailed data for each group
        async function loadDetailedData() {
            console.log('Loading detailed data for', dashboardData.length, 'groups');

            for (let group of dashboardData) {
                try {
                    // 【关键修改】使用 group_id 而不是 id，并添加 URL 编码
                    const result = await fetchAPI('/admin/model-groups/' + encodeURIComponent(group.group_id));
                    if (result && result.response.ok) {
                        group.details = result.data.data;
                        console.log('Loaded details for group:', group.group_id);
                    }
                } catch (error) {
                    console.error('Failed to load details for group', group.group_id, error);
                }
            }
        }

        // Render dashboard groups
        function renderDashboard() {
            const container = document.getElementById('groupsContainer');
            container.innerHTML = '';

            if (!dashboardData || dashboardData.length === 0) {
                container.innerHTML = '<div class="text-center py-12 text-gray-500">No model groups found. Click "Add Group" to create one.</div>';
                return;
            }

            dashboardData.forEach(function(group) {
                const groupCard = createGroupCard(group);
                container.appendChild(groupCard);
            });

            console.log('Rendered', dashboardData.length, 'group cards');
        }

        // Create group card element
        function createGroupCard(group) {
            const card = document.createElement('div');
            card.className = 'bg-white rounded-lg shadow-lg';

            const strategyBadge = group.strategy === 'round_robin'
                ? '<span class="px-3 py-1 text-sm rounded-full bg-green-100 text-green-800"><i class="fas fa-circle text-xs mr-1"></i>Round Robin</span>'
                : '<span class="px-3 py-1 text-sm rounded-full bg-blue-100 text-blue-800"><i class="fas fa-circle text-xs mr-1"></i>Fallback</span>';

            // 【关键修改】使用可选链和正确的检查
            const modelsHtml = group.details?.models && group.details.models.length > 0
                ? group.details.models.map((model, index) => createModelRow(model, index, group.id)).join('')
                : '<div class="text-center py-4 text-gray-500">No models found</div>';

            card.innerHTML =
                '<div class="px-6 py-4 border-b border-gray-200">' +
                    '<div class="flex items-center justify-between">' +
                        '<div>' +
                            '<h2 class="text-xl font-bold text-gray-800">' + (group.group_id || 'Unknown') + '</h2>' +
                            '<div class="flex items-center gap-3 mt-2">' +
                                strategyBadge +
                                '<span class="text-sm text-gray-500">' + (group.models || 0) + ' models</span>' +
                            '</div>' +
                        '</div>' +
                        '<div class="flex items-center gap-2">' +
                            '<button onclick="showAddModelModal(' + group.id + ')" class="text-blue-600 hover:text-blue-800 p-2" title="Add Model">' +
                                '<i class="fas fa-plus"></i>' +
                            '</button>' +
                            '<button onclick="deleteGroup(' + group.id + ')" class="text-red-600 hover:text-red-800 p-2" title="Delete Group">' +
                                '<i class="fas fa-trash"></i>' +
                            '</button>' +
                        '</div>' +
                    '</div>' +
                '</div>' +
                '<div class="p-6">' +
                    '<div class="space-y-4">' +
                        modelsHtml +
                    '</div>' +
                '</div>';

            return card;
        }

        // Create model row element
        function createModelRow(model, index, groupId) {
            const keysHtml = model.keys && model.keys.length > 0
                ? model.keys.map(key => createKeyElement(key)).join('')
                : '<div class="text-sm text-gray-500">No API keys</div>';

            // 【关键修改】使用循环索引 + 1 作为路由索引
            const routingIndex = index + 1;

            return '<div class="border rounded-lg p-4 hover:bg-gray-50 transition">' +
                '<div class="flex items-center justify-between mb-3">' +
                    '<div class="flex items-center gap-3">' +
                        '<span class="px-2 py-1 bg-gray-200 rounded text-sm font-mono">$' + routingIndex + '</span>' +
                        '<div>' +
                            '<div class="font-semibold text-gray-800">' + (model.provider_name || 'Unknown') + '</div>' +
                            '<div class="text-sm text-gray-600">' + (model.upstream_model || 'Unknown') + '</div>' +
                            '<div class="text-xs text-gray-500">' + (model.upstream_url || '') + '</div>' +
                        '</div>' +
                    '</div>' +
                    '<div class="flex items-center gap-2">' +
                        '<button onclick="editModel(' + model.id + ', \'' + (model.provider_name || '').replace(/'/g, "\\'") + '\', \'' + (model.upstream_url || '').replace(/'/g, "\\'") + '\', \'' + (model.upstream_model || '').replace(/'/g, "\\'") + '\', ' + (model.timeout || 30) + ')" class="text-blue-600 hover:text-blue-800 p-2" title="Edit Model">' +
                            '<i class="fas fa-edit"></i>' +
                        '</button>' +
                        '<button onclick="deleteModel(' + model.id + ')" class="text-red-600 hover:text-red-800 p-2" title="Delete Model">' +
                            '<i class="fas fa-trash"></i>' +
                        '</button>' +
                    '</div>' +
                '</div>' +
                '<div class="mt-3">' +
                    '<div class="text-xs text-gray-500 mb-1">API Keys (' + (model.keys_count || 0) + ')</div>' +
                    '<div class="flex flex-wrap gap-2">' +
                        keysHtml +
                        '<button onclick="showAddKeyModal(' + model.id + ')" class="px-2 py-1 bg-blue-100 text-blue-600 rounded text-xs hover:bg-blue-200" title="Add Key">' +
                            '<i class="fas fa-plus"></i>' +
                        '</button>' +
                    '</div>' +
                '</div>' +
            '</div>';
        }

        // Create key element with copy and delete buttons
        function createKeyElement(key) {
            return '<div class="relative group">' +
                '<span class="px-2 py-1 bg-gray-100 rounded text-xs font-mono key-hidden cursor-pointer" onclick="copyToClipboard(\'' + (key.full_key || key.key_value || '') + '\', this)">' +
                    (key.key_value || '') +
                '</span>' +
                '<button onclick="deleteKey(' + key.id + ')" class="absolute -top-2 -right-2 bg-red-500 text-white rounded-full w-5 h-5 text-xs hidden group-hover:block" title="Delete Key">' +
                    '<i class="fas fa-times"></i>' +
                '</button>' +
            '</div>';
        }

        // Update statistics
        function updateStats() {
            let totalGroups = dashboardData ? dashboardData.length : 0;
            let totalModels = 0;
            let totalKeys = 0;
            let totalRequests = 0;

            if (dashboardData) {
                dashboardData.forEach(function(group) {
                    totalModels += group.models || 0;
                    if (group.details?.models) {
                        group.details.models.forEach(function(model) {
                            totalKeys += model.keys_count || 0;
                            totalRequests += model.total_requests || 0;
                        });
                    }
                });
            }

            document.getElementById('totalGroups').textContent = totalGroups;
            document.getElementById('totalModels').textContent = totalModels;
            document.getElementById('totalKeys').textContent = totalKeys;
            document.getElementById('totalRequests').textContent = totalRequests;
        }

        // Refresh dashboard
        function refreshDashboard() {
            loadDashboard();
        }

        // Modal functions
        function showAddGroupModal() {
            document.getElementById('addGroupModal').classList.remove('hidden');
        }

        function showAddModelModal(groupId) {
            document.getElementById('addModelGroupId').value = groupId;
            document.getElementById('addModelModal').classList.remove('hidden');
        }

        function showAddKeyModal(modelId) {
            document.getElementById('addKeyModelId').value = modelId;
            document.getElementById('addKeyModal').classList.remove('hidden');
        }

        function closeModal(modalId) {
            document.getElementById(modalId).classList.add('hidden');
            // Reset form
            const form = document.querySelector('#' + modalId + ' form');
            if (form) form.reset();
        }

        // Form handlers
        async function handleAddGroup(e) {
            e.preventDefault();
            const formData = new FormData(e.target);
            const data = {
                group_id: formData.get('group_id'),
                strategy: formData.get('strategy')
            };

            try {
                const result = await fetchAPI('/admin/model-groups', {
                    method: 'POST',
                    body: JSON.stringify(data)
                });

                if (result && result.response.ok) {
                    showToast('Group added successfully', 'success');
                    closeModal('addGroupModal');
                    loadDashboard();
                } else {
                    showToast(result?.data?.error?.message || 'Failed to add group', 'error');
                }
            } catch (error) {
                showToast(error.message || 'Failed to add group', 'error');
            }
        }

        async function handleAddModel(e) {
            e.preventDefault();
            const formData = new FormData(e.target);
            const keysText = formData.get('keys').trim();
            const keys = keysText.split('\n').filter(k => k.trim());

            const data = {
                provider_name: formData.get('provider_name'),
                upstream_url: formData.get('upstream_url'),
                upstream_model: formData.get('upstream_model'),
                timeout: parseInt(formData.get('timeout')),
                keys: keys
            };

            const groupId = formData.get('group_id');
            try {
                const result = await fetchAPI('/admin/model-groups/' + groupId + '/models', {
                    method: 'POST',
                    body: JSON.stringify(data)
                });

                if (result && result.response.ok) {
                    showToast('Model added successfully', 'success');
                    closeModal('addModelModal');
                    loadDashboard();
                } else {
                    showToast(result?.data?.error?.message || 'Failed to add model', 'error');
                }
            } catch (error) {
                showToast(error.message || 'Failed to add model', 'error');
            }
        }

        async function handleAddKey(e) {
            e.preventDefault();
            const formData = new FormData(e.target);
            const data = {
                key: formData.get('key')
            };

            const modelId = formData.get('model_id');
            try {
                const result = await fetchAPI('/admin/models/' + modelId + '/keys', {
                    method: 'POST',
                    body: JSON.stringify(data)
                });

                if (result && result.response.ok) {
                    showToast('API key added successfully', 'success');
                    closeModal('addKeyModal');
                    loadDashboard();
                } else {
                    showToast(result?.data?.error?.message || 'Failed to add API key', 'error');
                }
            } catch (error) {
                showToast(error.message || 'Failed to add API key', 'error');
            }
        }

        // Edit Model functions
        function editModel(id, providerName, upstreamUrl, upstreamModel, timeout) {
            document.getElementById('editModelId').value = id;
            document.getElementById('editProviderName').value = providerName;
            document.getElementById('editUpstreamUrl').value = upstreamUrl;
            document.getElementById('editUpstreamModel').value = upstreamModel;
            document.getElementById('editTimeout').value = timeout;
            document.getElementById('editModelModal').classList.remove('hidden');
        }

        async function handleEditModel(e) {
            e.preventDefault();
            const formData = new FormData(e.target);
            const modelId = formData.get('id');

            const data = {
                provider_name: formData.get('provider_name'),
                upstream_url: formData.get('upstream_url'),
                upstream_model: formData.get('upstream_model'),
                timeout: parseInt(formData.get('timeout'))
            };

            try {
                const result = await fetchAPI('/admin/models/' + modelId, {
                    method: 'PUT',
                    body: JSON.stringify(data)
                });

                if (result && result.response.ok) {
                    showToast('Model updated successfully', 'success');
                    closeModal('editModelModal');
                    loadDashboard();
                } else {
                    showToast(result?.data?.error?.message || 'Failed to update model', 'error');
                }
            } catch (error) {
                showToast(error.message || 'Failed to update model', 'error');
            }
        }

        // Delete functions
        async function deleteGroup(groupId) {
            if (!confirm('Are you sure you want to delete this group and all its models?')) return;

            try {
                const result = await fetchAPI('/admin/model-groups/' + groupId, {
                    method: 'DELETE'
                });

                if (result && result.response.ok) {
                    showToast('Group deleted successfully', 'success');
                    loadDashboard();
                } else {
                    showToast(result?.data?.error?.message || 'Failed to delete group', 'error');
                }
            } catch (error) {
                showToast(error.message || 'Failed to delete group', 'error');
            }
        }

        async function deleteModel(modelId) {
            if (!confirm('Are you sure you want to delete this model?')) return;

            try {
                const result = await fetchAPI('/admin/models/' + modelId, {
                    method: 'DELETE'
                });

                if (result && result.response.ok) {
                    showToast('Model deleted successfully', 'success');
                    loadDashboard();
                } else {
                    showToast(result?.data?.error?.message || 'Failed to delete model', 'error');
                }
            } catch (error) {
                showToast(error.message || 'Failed to delete model', 'error');
            }
        }

        async function deleteKey(keyId) {
            if (!confirm('Are you sure you want to delete this API key?')) return;

            try {
                const result = await fetchAPI('/admin/keys/' + keyId, {
                    method: 'DELETE'
                });

                if (result && result.response.ok) {
                    showToast('API key deleted successfully', 'success');
                    loadDashboard();
                } else {
                    showToast(result?.data?.error?.message || 'Failed to delete API key', 'error');
                }
            } catch (error) {
                showToast(error.message || 'Failed to delete API key', 'error');
            }
        }

        // Copy to clipboard (with fallback)
        function copyToClipboard(text, element) {
            // Try modern clipboard API first
            if (navigator.clipboard) {
                navigator.clipboard.writeText(text).then(() => {
                    showCopySuccess(element);
                }).catch(() => {
                    fallbackCopy(text, element);
                });
            } else {
                fallbackCopy(text, element);
            }
        }

        function fallbackCopy(text, element) {
            // Create temporary textarea
            const textarea = document.createElement('textarea');
            textarea.value = text;
            textarea.style.position = 'fixed';
            textarea.style.left = '-999999px';
            document.body.appendChild(textarea);
            textarea.focus();
            textarea.select();

            try {
                document.execCommand('copy');
                showCopySuccess(element);
            } catch (err) {
                showToast('Failed to copy', 'error');
            }

            document.body.removeChild(textarea);
        }

        function showCopySuccess(element) {
            const originalText = element.textContent;
            element.textContent = 'Copied!';
            element.classList.add('text-green-600');
            setTimeout(() => {
                element.textContent = originalText;
                element.classList.remove('text-green-600');
            }, 2000);
        }

        // Toast notification
        function showToast(message, type) {
            const toast = document.getElementById('toast');
            const icon = document.getElementById('toastIcon');
            const msg = document.getElementById('toastMessage');

            msg.textContent = message;
            icon.className = type === 'success'
                ? 'fas fa-check-circle text-green-500 text-xl'
                : 'fas fa-exclamation-circle text-red-500 text-xl';

            toast.classList.remove('hidden');
            setTimeout(() => {
                toast.classList.add('hidden');
            }, 3000);
        }

        // 【优化】移除自动轮询，改为手动刷新模式
        // 避免日志刷屏，提升用户体验
    </script>
</body>
</html>`