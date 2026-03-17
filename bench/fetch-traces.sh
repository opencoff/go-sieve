#!/usr/bin/env bash
set -euo pipefail

# Base URLs
S3="https://s3.amazonaws.com/cache-datasets"
OG="cache_dataset_oracleGeneral"
DATADIR="$(cd "$(dirname "$0")/.." && pwd)/data"
DATADIR=`pwd`/data


die() { echo "FATAL: $*" >&2; exit 1; }

command -v zstd >/dev/null || die "zstd not found; install with: brew install zstd / apt install zstd"

# Prefer curl, fall back to wget
if command -v curl >/dev/null; then
    fetch() { curl -fSL --progress-bar --create-dirs -o "$2" "$1"; }
elif command -v wget >/dev/null; then
    fetch() { mkdir -p "$(dirname "$2")"; wget --show-progress -qO "$2" "$1"; }
else
    die "need curl or wget"
fi

# Download + decompress if not already done
get() {
    local url="$1" dest="$2"
    local raw="${dest%.zst}"
    local dn=`dirname $dest`
    mkdir -p $dn || die "can't mkdir $dn"

    [[ -f "$raw" ]] && return
    [[ -f "$dest" ]] || fetch "$url" "$dest"

    zstd -d --rm "$dest" -o "$raw"
}

mkdir -p $DATADIR || die "can't make $DATADIR"

# --- Meta Storage (Tectonic) — 5 block traces, ~70MB each ---
for i in $(seq 1 5); do
    bn="block_traces_${i}.oracleGeneral.bin.zst"
    dn="$S3/$OG/2022_metaStorage"

    get "$dn/$bn" "$DATADIR/meta_storage/$bn"
done

msr_files="\
msr_hm_0.oracleGeneral.zst \
msr_prn_0.oracleGeneral.zst \
msr_prn_1.oracleGeneral.zst \
msr_proj_0.oracleGeneral.zst \
msr_proj_1.oracleGeneral.zst \
msr_proj_2.oracleGeneral.zst \
msr_proj_4.oracleGeneral.zst \
msr_prxy_0.oracleGeneral.zst \
msr_prxy_1.oracleGeneral.zst \
msr_src1_0.oracleGeneral.zst \
msr_src1_1.oracleGeneral.zst \
msr_usr_1.oracleGeneral.zst \
msr_usr_2.oracleGeneral.zst \
msr_web_2.oracleGeneral.zst"

# --- MSR Cambridge — 2 selected volumes ---
for nm in $msr_files; do
    dn="$S3/$OG/2007_msr"
    get "$dn/$nm" "$DATADIR/msr_2007/$nm"
done

# --- Twitter cluster52 (CSV, subsample to 5M lines) ---
TWR_ZST="$DATADIR/twitter/cluster52.sort.zst"
TWR_CSV="$DATADIR/twitter/cluster52-5M.csv"
if [[ ! -f "$TWR_CSV" ]]; then
        [[ -f "$TWR_ZST" ]] || fetch \
            "https://ftp.pdl.cmu.edu/pub/datasets/twemcacheWorkload/open_source/cluster52.sort.zst" \
            "$TWR_ZST"
        zstd -d "$TWR_ZST" -c | head -5000000 > $TWR_CSV
fi
