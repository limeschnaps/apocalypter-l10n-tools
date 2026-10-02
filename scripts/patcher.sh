#!/bin/sh
# Installs a localization package into Apocalypter. Lists the *.lang files
# next to this script, asks for one and runs patcher on the game; a single
# package is installed without asking. The game
# folder (the one with Apocalypter_Data) is this script's folder or its
# parent, so the archive can be unpacked either way.
set -u

cd "$(dirname "$0")" || exit 1

data=
for dir in . ..; do
	if [ -d "$dir/Apocalypter_Data" ]; then
		data="$dir/Apocalypter_Data"
		break
	fi
done
if [ -z "$data" ]; then
	echo "Apocalypter_Data not found. Put these files into the folder with Apocalypter.exe." >&2
	exit 1
fi

set -- *.lang
if [ ! -e "$1" ]; then
	echo "No .lang files found next to $0." >&2
	exit 1
fi

if [ "$#" -gt 1 ]; then
	echo "Localization packages:"
	i=0
	for pkg in "$@"; do
		i=$((i + 1))
		echo "  $i) ${pkg%.lang}"
	done

	while :; do
		printf 'Select a package [1-%d, Enter = 1]: ' "$#"
		read -r choice || exit 1
		choice=${choice:-1}
		case $choice in
		*[!0-9]*) ;;
		*) [ "$choice" -ge 1 ] && [ "$choice" -le "$#" ] && break ;;
		esac
		echo "Enter a number from 1 to $#."
	done
	shift $((choice - 1))
fi
echo "Installing ${1%.lang}"

./patcher -package "$1" -in-place "$data"
