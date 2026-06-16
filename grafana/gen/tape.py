"""Tape & disk-pool infrastructure dashboard (API v12.0+, NBU 10.5+)."""

from grafana.gen import panels as p
from grafana.gen.variables import dashboard


def build():
    p.reset_ids()
    out = []

    # ── Row 1: Tape Drives ────────────────────────────────────────────────────
    out.append(p.row("Lecteurs bande / Tape Drives", 0))

    out.append(p.stat(
        "Lecteurs UP / Drives UP",
        'sum(nbu_tape_drives_count{status=~"DRIVE_STATUS_UP"})',
        0, 1, 6, 4,
        thresholds=[{"color": "green", "value": None}],
    ))
    out.append(p.stat(
        "Lecteurs DOWN / Drives DOWN",
        'sum(nbu_tape_drives_count{status=~"DRIVE_STATUS_DOWN"})',
        6, 1, 6, 4,
        thresholds=[
            {"color": "green", "value": None},
            {"color": "red", "value": 1},
        ],
    ))
    out.append(p.stat(
        "Total lecteurs / Total drives",
        'sum(nbu_tape_drives_count)',
        12, 1, 6, 4,
        thresholds=[{"color": "blue", "value": None}],
    ))
    out.append(p.piechart(
        "Lecteurs par statut / Drives by status",
        'sum by (status) (nbu_tape_drives_count)',
        18, 1, 6, 4,
        legend="{{status}}",
    ))

    out.append(p.barchart(
        "Lecteurs par type / Drives by type",
        'sum by (drive_type, robot_type, status) (nbu_tape_drives_count)',
        0, 5, 12, 8,
        legend="{{drive_type}} / {{robot_type}} / {{status}}",
    ))
    out.append(p.timeseries(
        "Évolution état lecteurs / Drive status over time",
        [
            p.target('sum by (status) (nbu_tape_drives_count)', "{{status}}"),
        ],
        12, 5, 12, 8,
    ))

    # ── Row 2: Tape Media ─────────────────────────────────────────────────────
    out.append(p.row("Inventaire cartouches / Tape Media", 13))

    out.append(p.stat(
        "Total cartouches / Total media",
        'sum(nbu_tape_media_count)',
        0, 14, 6, 4,
        thresholds=[{"color": "blue", "value": None}],
    ))
    out.append(p.piechart(
        "Cartouches par pool / Media by pool",
        'sum by (pool) (nbu_tape_media_count)',
        6, 14, 9, 8,
        legend="{{pool}}",
    ))
    out.append(p.piechart(
        "Cartouches par type / Media by type",
        'sum by (media_type) (nbu_tape_media_count)',
        15, 14, 9, 8,
        legend="{{media_type}}",
    ))

    out.append(p.barchart(
        "Cartouches par pool et type / Media by pool & type",
        'sum by (pool, media_type) (nbu_tape_media_count)',
        0, 18, 24, 8,
        legend="{{pool}} / {{media_type}}",
    ))

    # ── Row 3: Volume Pools ───────────────────────────────────────────────────
    out.append(p.row("Pools de volumes bande / Tape Volume Pools", 26))

    out.append(p.stat(
        "Cartouches partiellement pleines / Partially full media",
        'sum(nbu_tape_pool_partially_full)',
        0, 27, 8, 4,
        thresholds=[
            {"color": "green", "value": None},
            {"color": "yellow", "value": 5},
            {"color": "red", "value": 20},
        ],
    ))
    out.append(p.barchart(
        "Partiellement pleines par pool / Partially full by pool",
        'sum by (pool_name, pool_type) (nbu_tape_pool_partially_full)',
        8, 27, 16, 4,
        legend="{{pool_name}} ({{pool_type}})",
    ))

    out.append(p.timeseries(
        "Évolution cartouches partielles / Partially full media over time",
        [p.target('sum by (pool_name) (nbu_tape_pool_partially_full)', "{{pool_name}}")],
        0, 31, 24, 7,
    ))

    # ── Row 4: Disk Pool Volumes ──────────────────────────────────────────────
    out.append(p.row("Volumes disque / Disk Pool Volumes", 38))

    out.append(p.stat(
        "Volumes UP",
        'sum(nbu_disk_pool_volume_count{state="UP"})',
        0, 39, 6, 4,
        thresholds=[{"color": "green", "value": None}],
    ))
    out.append(p.stat(
        "Volumes DOWN",
        'sum(nbu_disk_pool_volume_count{state="DOWN"})',
        6, 39, 6, 4,
        thresholds=[
            {"color": "green", "value": None},
            {"color": "red", "value": 1},
        ],
    ))
    out.append(p.stat(
        "Volumes UNKNOWN",
        'sum(nbu_disk_pool_volume_count{state="UNKNOWN"})',
        12, 39, 6, 4,
        thresholds=[
            {"color": "green", "value": None},
            {"color": "orange", "value": 1},
        ],
    ))
    out.append(p.stat(
        "Total volumes disque / Total disk volumes",
        'sum(nbu_disk_pool_volume_count)',
        18, 39, 6, 4,
        thresholds=[{"color": "blue", "value": None}],
    ))

    out.append(p.barchart(
        "Volumes par pool et état / Volumes by pool & state",
        'sum by (pool_name, storage_category, state) (nbu_disk_pool_volume_count)',
        0, 43, 16, 8,
        legend="{{pool_name}} / {{storage_category}} / {{state}}",
    ))
    out.append(p.piechart(
        "Répartition état volumes / Volume state distribution",
        'sum by (state) (nbu_disk_pool_volume_count)',
        16, 43, 8, 8,
        legend="{{state}}",
    ))

    out.append(p.timeseries(
        "État volumes disque dans le temps / Disk volume state over time",
        [p.target('sum by (pool_name, state) (nbu_disk_pool_volume_count)',
                  "{{pool_name}} / {{state}}")],
        0, 51, 24, 7,
    ))

    return dashboard("nbu-tape", "NetBackup — Bande & Disques / Tape & Disk Pools", out)
