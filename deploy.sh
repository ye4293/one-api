#!/bin/bash

################################################################################
# One API 手动部署脚本
# 用途：使用 Docker 启动 MySQL 和 Redis，手动编译运行 one-api
################################################################################

set -e  # 遇到错误立即退出

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 日志函数
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 配置变量
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="${SCRIPT_DIR}"
BINARY_NAME="one-api"
LOG_DIR="${PROJECT_DIR}/logs"
DATA_DIR="${PROJECT_DIR}/data"
WEB_DIR="${PROJECT_DIR}/web"
BUILD_WEB="${BUILD_WEB:-true}"  # 是否构建前端，默认构建
WEB_THEME="${WEB_THEME:-default}"  # 前端主题，可选: default, berry, air

# 环境变量配置
export SQL_DSN="oneapi:123456@tcp(127.0.0.1:3306)/one-api"
export REDIS_CONN_STRING="redis://127.0.0.1:6379"
export SESSION_SECRET="${SESSION_SECRET:-random_string_$(date +%s)}"
export TZ="Asia/Shanghai"
export PORT="${PORT:-3000}"

# 检查必要的命令是否存在
check_dependencies() {
    log_info "检查依赖项..."
    
    local deps=("docker" "docker-compose" "go")
    local missing_deps=()
    
    for cmd in "${deps[@]}"; do
        if ! command -v "$cmd" &> /dev/null; then
            missing_deps+=("$cmd")
        fi
    done
    
    # 如果需要构建前端，检查 Node.js 和 npm
    if [ "$BUILD_WEB" = "true" ]; then
        if ! command -v node &> /dev/null; then
            missing_deps+=("node")
        fi
        if ! command -v npm &> /dev/null; then
            missing_deps+=("npm")
        fi
    fi
    
    if [ ${#missing_deps[@]} -ne 0 ]; then
        log_error "缺少以下依赖: ${missing_deps[*]}"
        log_info "请先安装缺失的依赖项"
        if [[ " ${missing_deps[@]} " =~ " node " ]] || [[ " ${missing_deps[@]} " =~ " npm " ]]; then
            log_info "或者设置 BUILD_WEB=false 跳过前端构建"
        fi
        exit 1
    fi
    
    log_success "依赖项检查完成"
    
    # 显示版本信息
    if [ "$BUILD_WEB" = "true" ]; then
        log_info "Node.js 版本: $(node --version)"
        log_info "npm 版本: $(npm --version)"
    fi
    log_info "Go 版本: $(go version | awk '{print $3}')"
    log_info "Docker 版本: $(docker --version | awk '{print $3}' | sed 's/,//')"
}

# 创建必要的目录
create_directories() {
    log_info "创建必要的目录..."
    
    mkdir -p "${LOG_DIR}"
    mkdir -p "${DATA_DIR}/mysql"
    mkdir -p "${DATA_DIR}/redis"
    mkdir -p "${DATA_DIR}/oneapi"
    mkdir -p "${WEB_DIR}/build"
    
    log_success "目录创建完成"
}

# 构建前端项目
build_web() {
    if [ "$BUILD_WEB" != "true" ]; then
        log_warning "跳过前端构建 (BUILD_WEB=false)"
        return 0
    fi
    
    log_info "开始构建前端项目..."
    
    cd "${WEB_DIR}"
    
    # 检查主题是否存在
    if [ ! -d "${WEB_THEME}" ]; then
        log_error "主题目录不存在: ${WEB_THEME}"
        log_info "可用主题: default, berry, air"
        exit 1
    fi
    
    log_info "构建主题: ${WEB_THEME}"
    
    cd "${WEB_THEME}"
    
    # 安装依赖
    if [ ! -d "node_modules" ]; then
        log_info "安装 npm 依赖..."
        npm install --legacy-peer-deps
    else
        log_info "npm 依赖已存在，跳过安装"
    fi
    
    # 构建项目
    log_info "执行构建..."
    DISABLE_ESLINT_PLUGIN='true' npm run build
    
    if [ $? -ne 0 ]; then
        log_error "前端构建失败"
        exit 1
    fi
    
    # 检查构建产物
    if [ -d "../build/${WEB_THEME}" ]; then
        log_success "前端构建完成: web/build/${WEB_THEME}"
    else
        log_error "构建产物未找到"
        exit 1
    fi
    
    cd "${PROJECT_DIR}"
}

# 清理前端构建产物
clean_web() {
    log_info "清理前端构建产物..."
    
    if [ -d "${WEB_DIR}/build" ]; then
        rm -rf "${WEB_DIR}/build"
        log_success "清理完成"
    else
        log_info "无需清理"
    fi
}

# 启动 Docker 依赖服务
start_docker_services() {
    log_info "启动 Docker 依赖服务（MySQL 和 Redis）..."
    
    cd "${PROJECT_DIR}"
    
    if [ ! -f "docker-compose-deps.yml" ]; then
        log_error "未找到 docker-compose-deps.yml 文件"
        exit 1
    fi
    
    docker-compose -f docker-compose-deps.yml up -d
    
    log_success "Docker 服务启动命令已执行"
}

# 等待服务就绪
wait_for_services() {
    log_info "等待数据库服务就绪..."
    
    local max_attempts=30
    local attempt=0
    
    # 等待 MySQL
    while [ $attempt -lt $max_attempts ]; do
        if docker exec one-api-mysql mysqladmin ping -h localhost -u root -pOneAPI@justsong &> /dev/null; then
            log_success "MySQL 已就绪"
            break
        fi
        attempt=$((attempt + 1))
        echo -n "."
        sleep 2
    done
    
    if [ $attempt -eq $max_attempts ]; then
        log_error "MySQL 启动超时"
        exit 1
    fi
    
    echo ""
    
    # 等待 Redis
    attempt=0
    log_info "等待 Redis 服务就绪..."
    while [ $attempt -lt $max_attempts ]; do
        if docker exec one-api-redis redis-cli ping &> /dev/null; then
            log_success "Redis 已就绪"
            break
        fi
        attempt=$((attempt + 1))
        echo -n "."
        sleep 2
    done
    
    if [ $attempt -eq $max_attempts ]; then
        log_error "Redis 启动超时"
        exit 1
    fi
    
    echo ""
    log_success "所有依赖服务已就绪"
}

# 编译 Go 项目
build_project() {
    log_info "开始编译 one-api..."
    
    cd "${PROJECT_DIR}"
    
    if [ ! -f "go.mod" ]; then
        log_error "未找到 go.mod 文件，请确认在正确的项目目录"
        exit 1
    fi
    
    # 下载依赖
    log_info "下载 Go 依赖..."
    go mod download
    
    # 编译
    log_info "编译二进制文件..."
    go build -o "${BINARY_NAME}" -ldflags="-s -w" .
    
    if [ ! -f "${BINARY_NAME}" ]; then
        log_error "编译失败"
        exit 1
    fi
    
    chmod +x "${BINARY_NAME}"
    log_success "编译完成: ${BINARY_NAME}"
}

# 构建所有项目（前端 + 后端）
build_all() {
    build_web
    build_project
}

# 停止现有的 one-api 进程
stop_existing_process() {
    log_info "检查现有进程..."
    
    local pid=$(pgrep -f "${BINARY_NAME}" || true)
    
    if [ -n "$pid" ]; then
        log_warning "发现运行中的进程 (PID: $pid)，正在停止..."
        kill "$pid" 2>/dev/null || true
        sleep 2
        
        # 强制杀死
        if pgrep -f "${BINARY_NAME}" > /dev/null; then
            log_warning "强制停止进程..."
            pkill -9 -f "${BINARY_NAME}" || true
            sleep 1
        fi
        
        log_success "已停止现有进程"
    fi
}

# 启动 one-api 服务
start_service() {
    log_info "启动 one-api 服务..."
    
    cd "${PROJECT_DIR}"
    
    # 导出环境变量
    export SQL_DSN="${SQL_DSN}"
    export REDIS_CONN_STRING="${REDIS_CONN_STRING}"
    export SESSION_SECRET="${SESSION_SECRET}"
    export TZ="${TZ}"
    export PORT="${PORT}"
    
    # 后台运行
    nohup ./"${BINARY_NAME}" --log-dir "${LOG_DIR}" > "${LOG_DIR}/oneapi.out" 2>&1 &
    local pid=$!
    
    echo "$pid" > "${PROJECT_DIR}/one-api.pid"
    
    log_success "one-api 已启动 (PID: $pid)"
    log_info "日志目录: ${LOG_DIR}"
    log_info "访问地址: http://localhost:${PORT}"
    
    # 等待服务启动
    sleep 3
    
    # 检查进程是否运行
    if ps -p $pid > /dev/null; then
        log_success "服务运行正常"
    else
        log_error "服务启动失败，请查看日志: ${LOG_DIR}/oneapi.out"
        exit 1
    fi
}

# 显示服务状态
show_status() {
    echo ""
    echo "========================================"
    log_info "服务状态"
    echo "========================================"
    
    # Docker 服务状态
    echo ""
    log_info "Docker 服务:"
    docker-compose -f docker-compose-deps.yml ps
    
    # one-api 进程状态
    echo ""
    log_info "one-api 进程:"
    if [ -f "${PROJECT_DIR}/one-api.pid" ]; then
        local pid=$(cat "${PROJECT_DIR}/one-api.pid")
        if ps -p $pid > /dev/null 2>&1; then
            echo -e "  状态: ${GREEN}运行中${NC} (PID: $pid)"
        else
            echo -e "  状态: ${RED}已停止${NC}"
        fi
    else
        echo -e "  状态: ${RED}未运行${NC}"
    fi
    
    echo ""
    echo "========================================"
    log_info "配置信息"
    echo "========================================"
    echo "  数据库: ${SQL_DSN}"
    echo "  Redis: ${REDIS_CONN_STRING}"
    echo "  端口: ${PORT}"
    echo "  日志目录: ${LOG_DIR}"
    echo "  数据目录: ${DATA_DIR}"
    echo "  前端主题: ${WEB_THEME}"
    if [ -d "${WEB_DIR}/build/${WEB_THEME}" ]; then
        echo -e "  前端状态: ${GREEN}已构建${NC}"
    else
        echo -e "  前端状态: ${YELLOW}未构建${NC}"
    fi
    echo ""
}

# 停止所有服务
stop_all() {
    log_info "停止所有服务..."
    
    # 停止 one-api
    stop_existing_process
    
    # 停止 Docker 服务
    log_info "停止 Docker 服务..."
    cd "${PROJECT_DIR}"
    docker-compose -f docker-compose-deps.yml down
    
    log_success "所有服务已停止"
}

# 重启服务
restart_service() {
    log_info "重启 one-api 服务..."
    stop_existing_process
    sleep 2
    start_service
}

# 查看日志
view_logs() {
    log_info "查看 one-api 日志 (Ctrl+C 退出)..."
    tail -f "${LOG_DIR}/oneapi.out"
}

# 显示帮助信息
show_help() {
    cat << EOF
One API 部署脚本

用法:
    $0 [命令]

命令:
    start           启动所有服务（Docker 依赖 + one-api）
    stop            停止所有服务
    restart         重启 one-api 服务
    status          显示服务状态
    logs            查看 one-api 日志
    build           编译项目（前端 + 后端）
    build-web       仅构建前端项目
    build-go        仅编译后端项目
    clean-web       清理前端构建产物
    help            显示此帮助信息

环境变量:
    PORT                  服务端口（默认: 3000）
    SESSION_SECRET        会话密钥（默认: 自动生成）
    SQL_DSN              数据库连接（默认: oneapi:123456@tcp(127.0.0.1:3306)/one-api）
    REDIS_CONN_STRING    Redis 连接（默认: redis://127.0.0.1:6379）
    BUILD_WEB            是否构建前端（默认: true）
    WEB_THEME            前端主题（默认: default，可选: default, berry, air）

示例:
    # 启动所有服务（自动构建前端和后端）
    $0 start
    
    # 使用 berry 主题启动
    WEB_THEME=berry $0 start
    
    # 跳过前端构建启动
    BUILD_WEB=false $0 start
    
    # 自定义端口启动
    PORT=8080 $0 start
    
    # 仅构建前端（air 主题）
    WEB_THEME=air $0 build-web
    
    # 构建所有主题
    for theme in default berry air; do
        WEB_THEME=\$theme $0 build-web
    done
    
    # 查看日志
    $0 logs
    
    # 停止服务
    $0 stop

主题说明:
    default    - 默认主题，简洁轻量
    berry      - Material-UI 风格，功能丰富
    air        - 现代化设计，轻量快速

EOF
}

# 主函数
main() {
    case "${1:-}" in
        start)
            check_dependencies
            create_directories
            start_docker_services
            wait_for_services
            build_all
            stop_existing_process
            start_service
            show_status
            ;;
        stop)
            stop_all
            ;;
        restart)
            restart_service
            show_status
            ;;
        status)
            show_status
            ;;
        logs)
            view_logs
            ;;
        build)
            check_dependencies
            create_directories
            build_all
            ;;
        build-web)
            check_dependencies
            create_directories
            build_web
            ;;
        build-go)
            check_dependencies
            build_project
            ;;
        clean-web)
            clean_web
            ;;
        help|--help|-h)
            show_help
            ;;
        *)
            log_error "未知命令: ${1:-}"
            echo ""
            show_help
            exit 1
            ;;
    esac
}

# 执行主函数
main "$@"

