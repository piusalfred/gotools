# Copyright (c) 2026 Pius Alfred
# License: MIT


# ---- uninstall -----------------------------------------------------------



cmd_uninstall() {
    echo "⚠️  WARNING: This will delete the 'gotools.sh' script itself."
    printf "Do you want to uninstall gotools.sh? (y/N): "
    read -r confirmation

    if [[ "$confirmation" =~ ^[Yy]$ ]]; then
        local script_path
        script_path="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
        rm -f "$script_path"
        echo "✅ gotools.sh has been uninstalled. Goodbye!"
        exit 0
    else
        echo "❌ Uninstall cancelled."
    fi
}
