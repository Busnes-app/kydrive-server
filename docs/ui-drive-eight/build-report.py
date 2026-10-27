"""Validate immutable KyDrive Council reports; export only this directory's CSV/XLSX.

Run from any directory with Python 3. No publisher, audit or product file is written.
Exact decimals use parentheses for recurring digits; fractions remain authoritative.
"""
from collections import defaultdict
from decimal import Decimal, ROUND_HALF_UP, localcontext
from fractions import Fraction
from itertools import combinations
from pathlib import Path
from xml.sax.saxutils import escape
import csv
import hashlib
import json
import re
import struct
import zipfile
import xml.etree.ElementTree as ET

P = Path(__file__).resolve().parent
WEIGHTS = dict(hierarchy='.20', typography='.15', density_layout='.20',
               color='.15', visible_affordances='.15', coherence='.15')
BRAND = ('palette_tokens', 'type_geometry', 'shell', 'accent_discipline', 'suite_cohesion')
REVIEWERS = ('opus', 'sol', 'gemini')
NA = (None, 'N/A')
SUMMARY = ['n', 'Sum fraction', 'Sum exact decimal', 'Mean fraction',
           'Mean exact decimal (parentheses repeat)', 'Half-up 0.1']


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def load(path):
    return json.loads(path.read_text(encoding='utf-8'))


def number(value):
    return Fraction(str(value))


def decimal28(value):
    with localcontext() as ctx:
        ctx.prec = 28
        return Decimal(value.numerator) / Decimal(value.denominator)


def rounded(value):
    return str(decimal28(value).quantize(Decimal('.1'), rounding=ROUND_HALF_UP))


def exact_decimal(value):
    """Long division preserves finite and recurring decimals without float loss."""
    sign = '-' if value < 0 else ''
    whole, remainder = divmod(abs(value.numerator), value.denominator)
    digits, seen = [], {}
    while remainder and remainder not in seen:
        seen[remainder] = len(digits)
        digit, remainder = divmod(remainder * 10, value.denominator)
        digits.append(str(digit))
    if remainder:
        start = seen[remainder]
        digits = digits[:start] + ['('] + digits[start:] + [')']
    return sign + str(whole) + ('.' + ''.join(digits) if digits else '')


def stats(values):
    if not values:
        return [0, 'N/A', 'N/A', 'N/A', 'N/A', 'N/A']
    total = sum(values, Fraction())
    mean = total / len(values)
    return [len(values), str(total), exact_decimal(total), str(mean),
            exact_decimal(mean), rounded(mean)]


def validate_summary(published, values, label):
    require(values, f'{label}: empty published summary')
    total = sum(values, Fraction())
    mean = total / len(values)
    require(published['n'] == len(values), f'{label}: count mismatch')
    for key in ('sum', 'sum_of_file_means', 'sum_of_exact_file_means_fraction'):
        if key in published:
            require(number(published[key]) == total, f'{label}: {key} mismatch')
    if 'fraction' in published:
        require(number(published['fraction']) == mean, f'{label}: fraction mismatch')
    if 'decimal' in published:
        require(Decimal(str(published['decimal'])) == decimal28(mean),
                f'{label}: published decimal mismatch')
    if 'half_up_0_1' in published:
        require(str(published['half_up_0_1']) == rounded(mean), f'{label}: rounding mismatch')


def images(rows, label):
    pins = {row['file']: row for row in rows}
    require(len(pins) == len(rows), f'{label}: duplicate paths')
    for name, row in pins.items():
        path = (P / name).resolve()
        require(path.is_relative_to(P) and path.suffix == '.png', f'{label}: invalid path {name}')
        data = path.read_bytes()
        require(data[:8] == b'\x89PNG\r\n\x1a\n' and data[12:16] == b'IHDR', f'{name}: not PNG')
        require(sha(data) == row['sha256'] and len(data) == row['bytes'], f'{name}: hash/bytes mismatch')
        require(struct.unpack('>II', data[16:24]) == (row['width'], row['height']),
                f'{name}: dimensions mismatch')
    return pins


def vector(scores, keys, label, allow_na=False):
    require(isinstance(scores, dict) and set(scores) == set(keys), f'{label}: dimensions mismatch')
    require(all((allow_na and value in NA) or
                (type(value) is int and 0 <= value <= 10) for value in scores.values()),
            f'{label}: expected integer dimensions 0..10')


def read_report(path, inventories):
    match = re.fullmatch(r'(opus|sol|gemini)-round([1-9]\d*)\.json', path.name)
    who, round_no = match[1], int(match[2])
    raw = path.read_bytes()
    report = json.loads(raw)
    require(report['schema'] == 'ky-ui-council-scores/2', f'{path.name}: schema mismatch')
    require(report['rubric']['version'] == 'ky-suite-v2', f'{path.name}: rubric mismatch')
    require({k: number(v) for k, v in report['rubric']['weights'].items()} ==
            {k: number(v) for k, v in WEIGHTS.items()}, f'{path.name}: weights mismatch')
    files = {row['file']: row for row in report['files']}
    require(len(files) == len(report['files']), f'{path.name}: repeated file')
    matches = [(name, pins) for name, pins in inventories if pins.keys() == files.keys()]
    require(matches, f'{path.name}: file list differs from every inventory-v*.json')
    inventory_name, pins = matches[0]
    declared_inventory = report['inventory'].get('quality_source') if isinstance(report['inventory'], dict) else None
    if declared_inventory:
        require(any(name == declared_inventory for name, _ in matches), f'{path.name}: wrong inventory')
        inventory_name, pins = next(item for item in matches if item[0] == declared_inventory)
    frozen = sha(json.dumps(report['files'], sort_keys=True, separators=(',', ':'),
                            ensure_ascii=False).encode('utf-8'))
    freeze = report.get('freeze', report.get('blind_freeze', {}))
    require(frozen == freeze.get('sha256', freeze.get('files_sha256')), f'{path.name}: complete files freeze mismatch')
    if 'files_count' in freeze:
        require(freeze['files_count'] == len(files), f'{path.name}: freeze count mismatch')
    totals, brands, cohorts, surfaces = [], [], defaultdict(list), defaultdict(list)
    brand_cohorts, brand_surfaces, brand_dimensions = defaultdict(list), defaultdict(list), defaultdict(list)
    rows = []
    for name in sorted(files):
        row, pin = files[name], pins[name]
        for key in ('sha256', 'width', 'height', 'bytes'):
            if key in row or key != 'bytes':
                require(row[key] == pin[key], f'{path.name}/{name}: {key} mismatch')
        require(row.get('duplicate_of') == pin.get('duplicate_of'), f'{name}: duplicate declaration mismatch')
        duplicate = row.get('duplicate_of')
        if duplicate:
            require(duplicate in files and not files[duplicate].get('duplicate_of'), f'{name}: invalid duplicate target')
            require(row['scores'] == files[duplicate]['scores'] and row['total'] == files[duplicate]['total']
                    and row.get('ky_fit') == files[duplicate].get('ky_fit'), f'{name}: duplicate did not inherit scores')
        surface = pin.get('surface') or row.get('surface_key', row.get('surface'))
        require(surface and row['cohort'], f'{name}: missing surface/cohort')
        included = not duplicate and row['total'] not in NA
        if row['total'] in NA:
            require(row['scores'] in NA or all(v in NA for v in row['scores'].values()),
                    f'{name}: N/A quality has scored dimensions')
            weighted, total = None, None
        else:
            vector(row['scores'], WEIGHTS, name)
            weighted = sum(number(weight) * row['scores'][key] for key, weight in WEIGHTS.items())
            total = number(rounded(weighted))
            require(total == number(row['total']), f'{name}: weighted total mismatch')
            for key in ('weighted_raw', 'quality_weighted_unrounded'):
                if key in row:
                    require(number(row[key]) == weighted, f'{name}: {key} mismatch')
        fit = row['ky_fit']
        vector(fit['scores'], BRAND, name + '/brand', allow_na=True)
        brand_values = [number(v) for v in fit['scores'].values() if v not in NA]
        excluded = {key for key, value in fit['scores'].items() if value in NA}
        require(set(fit['na_excluded']) == excluded, f'{name}: brand N/A exclusion mismatch')
        brand = sum(brand_values, Fraction()) / len(brand_values) if brand_values else None
        for key in ('mean_exact', 'fraction'):
            if key in fit:
                require(brand is not None and number(fit[key]) == brand, f'{name}: brand exact mean mismatch')
        if 'exact_decimal' in fit:
            require(brand is not None and Decimal(str(fit['exact_decimal'])) == decimal28(brand), f'{name}: brand decimal mismatch')
        for key in ('mean_half_up_0_1', 'total'):
            if key in fit:
                require(brand is not None and number(fit[key]) == number(rounded(brand)), f'{name}: brand rounding mismatch')
        if 'n' in fit:
            require(fit['n'] == len(brand_values) and number(fit['sum']) == sum(brand_values, Fraction()),
                    f'{name}: brand n/sum mismatch')
        if included:
            totals.append(total)
            cohorts[row['cohort']].append(total)
            surfaces[surface].append(total)
        if not duplicate and brand is not None:
            brands.append(brand)
            brand_cohorts[row['cohort']].append(brand)
            brand_surfaces[surface].append(brand)
            for key, value in fit['scores'].items():
                if value not in NA:
                    brand_dimensions[key].append(number(value))
        dimensions = [row['scores'][key] if total is not None else 'N/A' for key in WEIGHTS]
        rows.append([round_no, who, name, inventory_name, surface, row['cohort'], duplicate or '', included,
                     *dimensions, str(weighted) if weighted is not None else 'N/A',
                     str(row['total']) if total is not None else 'N/A',
                     *[fit['scores'][key] if fit['scores'][key] not in NA else 'N/A' for key in BRAND],
                     *stats(brand_values), sha(raw)])
    validate_summary(report['overall_covered_set'], totals, path.name + '/overall')
    published_cohorts = report.get('cohort_summaries', report['cohort_rankings'])
    require(set(published_cohorts) == set(cohorts), f'{path.name}: cohort set mismatch')
    for key, values in cohorts.items():
        validate_summary(published_cohorts[key], values, path.name + '/' + key)
    if 'surface_summaries' in report:
        require(set(report['surface_summaries']) == set(surfaces), f'{path.name}: surface set mismatch')
        for key, values in surfaces.items():
            validate_summary(report['surface_summaries'][key], values, path.name + '/' + key)
    if 'brand_covered_set' in report:
        validate_summary(report['brand_covered_set'], brands, path.name + '/brand')
    if 'brand' in report:
        # Preserve publishers that explicitly aggregate rounded per-file brand means;
        # the exported brand statistics still use the exact dimension-derived values.
        rounded_brand = 'Summary = mean of per-file rounded means' in report['brand'].get('method', '')
        published_values = lambda values: [number(rounded(v)) for v in values] if rounded_brand else values
        if rounded_brand:
            for row in files.values():
                values = [number(v) for v in row['ky_fit']['scores'].values() if v not in NA]
                require(number(row['ky_fit']['mean']) == number(rounded(sum(values, Fraction()) / len(values))),
                        f'{path.name}: published rounded brand mean mismatch')
        validate_summary(report['brand']['overall'], published_values(brands), path.name + '/brand')
        require(set(report['brand']['by_cohort']) == set(brand_cohorts),
                f'{path.name}: brand summary cohorts mismatch')
        for key, values in brand_cohorts.items():
            validate_summary(report['brand']['by_cohort'][key], published_values(values), path.name + '/brand/' + key)
        if 'by_dimension' in report['brand']:
            require(set(report['brand']['by_dimension']) == set(brand_dimensions),
                    f'{path.name}: brand summary dimensions mismatch')
        for key, summary in report['brand'].get('by_dimension', {}).items():
            values = brand_dimensions[key]
            validate_summary(summary, values, path.name + '/brand/' + key)
            require(summary['na'] == len(brands) - len(values), f'{key}: brand N/A count mismatch')
    verdict = report['verdict']
    require(all(type(verdict[key]) is bool for key in ('satisfied', 'genuinely_satisfied')),
            f'{path.name}: satisfaction must be explicit booleans')
    if 'independent_satisfaction' in report:
        require(all(report['independent_satisfaction'][key] == verdict[key]
                    for key in ('satisfied', 'genuinely_satisfied')), f'{path.name}: conflicting satisfaction')
    return dict(who=who, round=round_no, report=report, files=files, totals=totals, brands=brands,
                cohorts=cohorts, surfaces=surfaces, brand_cohorts=brand_cohorts,
                brand_surfaces=brand_surfaces, brand_dimensions=brand_dimensions, rows=rows,
                mean=sum(totals, Fraction()) / len(totals), inventory=inventory_name, freeze=frozen)


def workbook(sheets):
    """ponytail: string cells keep fractions, long decimals and hashes lossless."""
    ns = 'http://schemas.openxmlformats.org/spreadsheetml/2006/main'
    rel = 'http://schemas.openxmlformats.org/package/2006/relationships'
    office = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'
    parts = {
        '[Content_Types].xml': '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
        '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
        '<Default Extension="xml" ContentType="application/xml"/>'
        '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>'
        + ''.join(f'<Override PartName="/xl/worksheets/sheet{i}.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>'
                  for i in range(1, len(sheets) + 1)) + '</Types>',
        '_rels/.rels': f'<Relationships xmlns="{rel}"><Relationship Id="rId1" Type="{office}/officeDocument" Target="xl/workbook.xml"/></Relationships>',
        'xl/workbook.xml': f'<workbook xmlns="{ns}" xmlns:r="{office}"><sheets>'
        + ''.join(f'<sheet name="{name}" sheetId="{i}" r:id="rId{i}"/>' for i, name in enumerate(sheets, 1)) + '</sheets></workbook>',
        'xl/_rels/workbook.xml.rels': f'<Relationships xmlns="{rel}">'
        + ''.join(f'<Relationship Id="rId{i}" Type="{office}/worksheet" Target="worksheets/sheet{i}.xml"/>' for i in range(1, len(sheets) + 1)) + '</Relationships>',
    }
    for i, rows in enumerate(sheets.values(), 1):
        parts[f'xl/worksheets/sheet{i}.xml'] = f'<worksheet xmlns="{ns}"><sheetData>' + ''.join(
            f'<row r="{j}">' + ''.join('<c t="inlineStr"><is><t xml:space="preserve">'
                                      + escape(str(value)) + '</t></is></c>' for value in row) + '</row>'
            for j, row in enumerate(rows, 1)) + '</sheetData></worksheet>'
    path = P / 'KyDrive-Council.xlsx'
    with zipfile.ZipFile(path, 'w', zipfile.ZIP_DEFLATED) as archive:
        for name, xml in parts.items():
            ET.fromstring(xml)
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(info, xml.encode('utf-8'))
    with zipfile.ZipFile(path) as archive:
        require(archive.testzip() is None, 'XLSX CRC failure')
        for name in archive.namelist():
            ET.fromstring(archive.read(name))
        for i, rows in enumerate(sheets.values(), 1):
            root = ET.fromstring(archive.read(f'xl/worksheets/sheet{i}.xml'))
            actual = [[cell.find(f'{{{ns}}}is/{{{ns}}}t').text or '' for cell in row]
                      for row in root.findall(f'{{{ns}}}sheetData/{{{ns}}}row')]
            require(actual == [[str(value) for value in row] for row in rows], 'XLSX data round-trip mismatch')


def main():
    authority = load(P / 'authority.json')
    for name, digest in authority['files'].items():
        require(sha((P / authority['local_directory'] / name).read_bytes()) == digest, f'Authority changed: {name}')
    inventories = [(path.name, images(load(path), path.name)) for path in sorted(P.glob('inventory-v*.json'))]
    require(inventories, 'No inventory-v*.json')
    artifacts = images(load(P / 'artifacts.json'), 'artifacts.json')
    refs = load(P / 'brand-references.json')
    for ref in refs:
        require(artifacts[ref['file']]['sha256'] == ref['sha256'], 'Brand reference pin mismatch')
    paths = [path for path in P.glob('*-round*.json')
             if re.fullmatch(r'(opus|sol|gemini)-round[1-9]\d*\.json', path.name)]
    frozen_reports = {path: sha(path.read_bytes()) for source in paths for path in (source, source.with_suffix('.md'))}
    reports = {}
    for path in sorted(paths):
        result = read_report(path, inventories)
        reports[result['who'], result['round']] = result
        if result['round'] == 1:
            files = result['files']
            require(set(files) | {r['file'] for r in refs} == set(artifacts), 'R1 artifact set mismatch')
            unscored = result['report'].get('unscored', result['report']['inventory'].get('unscored', []))
            require({r['file'] for r in unscored} == {r['file'] for r in refs}, 'R1 unscored reference set mismatch')
            for row in unscored:
                require(row['sha256'] == artifacts[row['file']]['sha256'] and row['reason'], 'Unscored reference mismatch')
    require(reports, 'No published report pairs')
    for who, num in reports:
        if who == 'gemini':
            require(all((primary, num) in reports and
                        reports[primary, num]['report']['verdict']['genuinely_satisfied']
                        for primary in ('opus', 'sol')),
                    f'R{num}: Gemini published before both primary approvals')
    delivery = load(P / 'delivery.json') if (P / 'delivery.json').exists() else {}
    numbers = {num for _, num in reports}
    if 'review_round' in delivery:
        value = str(delivery['review_round'])
        match = re.fullmatch(r'(?:round)?([1-9]\d*)', value)
        require(match, 'delivery.json review_round must be N or roundN')
        numbers.add(int(match[1]))
    current = max(numbers)
    scores = [['Round', 'Reviewer', 'File', 'Inventory', 'Surface', 'Cohort', 'Duplicate of', 'Quality included',
               *WEIGHTS, 'Weighted unrounded fraction', 'Quality total', *['Brand ' + k for k in BRAND],
               *['Brand dimensions ' + k for k in SUMMARY], 'Report SHA256']]
    cohorts = [['Round', 'Reviewer', 'Cohort', 'Measure', *SUMMARY, 'Satisfied', 'Genuinely satisfied']]
    surfaces = [['Round', 'Reviewer', 'Surface', 'Measure', *SUMMARY, 'Satisfied', 'Genuinely satisfied']]
    progression = [['Round', 'Current', 'Reviewer', 'Status', *SUMMARY, 'Satisfied', 'Genuinely satisfied',
                    'Opus rule result for this round', 'Current owner progression gate']]
    spreads = [['Round', 'File', 'Reviewer A', 'Reviewer B', 'Measure', 'Dimension', 'A', 'B', 'Absolute spread', 'Reinspection required (>=2)']]
    notes = [['Topic', 'Evidence'],
             ['Scope', '40 R1 initial-view originals; actual local administrator on authenticated surfaces and signed-out login. Covered-set descriptions, not rankings. No whole-product, competitive or behavior acceptance claim.'],
             ['Missing/unscored', 'Ordinary users, group-shared workspaces, populated Shared with me, Euro-Office editor/save, live SSO, guests and connected workflows remain missing/unscored. No inferred zero scores.'],
             ['Conditions', 'R1 source fd472e8035222ae38d8bb4b5c5cbd79c066879a1; evidence 0354de843b0503b9ff52b51318ab214867b9e0e8. See immutable manifest-00-before.json and baseline-setup.md: 1280x900 / 390x844, DPR 1, OS light/dark, no saved theme, normal service workers; phone-sized desktop Chromium, not physical device.'],
             ['Source/build warnings', 'baseline-setup.md records baseline build/setup evidence and warnings (React peer range, sliced deprecation, optional watcher script and Vite chunk size). Exporter does not build, recapture or exercise product tests; parent owns source/full verification.'],
             ['Progression', 'ONLY current-round Opus overall quality mean over unique scored rounded file totals, half-up to 0.1 >= 8.0; 7.985 counts as 8.0. No combined reviewer mean, brand weight, historical pooling or cohort veto. Owner rejection reopens the gate. 9/10 remains unproven.'],
             ['Arithmetic', 'All computations use Fraction/Decimal; exact decimal parentheses mark recurring digits. Published decimal fields checked at Decimal precision 28. Brand means exclude N/A dimensions and duplicate files, remain independent and never enter quality/gate. Text XLSX cells preserve precision.'],
             ['Gemini', 'Unused until BOTH primaries genuinely_satisfied=true. Current quota unavailable; no retry, invented scores or advisory delay to accepted progression.'],
             ['Disagreements', 'Every quality/brand dimension is compared independently for every same-round reviewer pair, including equal/N/A values. >=2 requires reinspection by parent/audit owner, never averaging. This exporter neither reads nor writes adjudication files.'],
             ['Gates', 'Full behavior/accessibility/security/role/integration gates remain untested in R1 screenshot review. Scores do not establish those gates.'],
             ['DOX', 'Existing root/docs/ui-drive-eight contracts already own exports and evidence limits; AGENTS unchanged.'],
             ['MySlop', 'No post/retry per explicit instruction: known HTTP 401.']]
    for (who, num), result in sorted(reports.items(), key=lambda item: (item[0][1], REVIEWERS.index(item[0][0]))):
        r = result['report']
        satisfied = [r['verdict']['satisfied'], r['verdict']['genuinely_satisfied']]
        scores.extend(result['rows'])
        for sheet, field in ((cohorts, 'cohorts'), (surfaces, 'surfaces')):
            for measure, groups in [('Quality', result[field]), ('Brand', result['brand_' + field])]:
                for group, values in sorted(groups.items()):
                    sheet.append([num, who, group, measure, *stats(values), *satisfied])
        notes.extend([[f'R{num}/{who}/reviewer', json.dumps(r['reviewer'], ensure_ascii=False, sort_keys=True)],
                      [f'R{num}/{who}/freeze', result['freeze']],
                      [f'R{num}/{who}/inventory', result['inventory']],
                      [f'R{num}/{who}/satisfaction', json.dumps(r['verdict'], ensure_ascii=False, sort_keys=True)],
                      [f'R{num}/{who}/source', json.dumps(r.get('source', {}), ensure_ascii=False, sort_keys=True)]])
        notes.append([f'R{num}/{who}/brand covered set', json.dumps(dict(zip(SUMMARY, stats(result['brands']))))])
        for key, values in sorted(result['brand_dimensions'].items()):
            notes.append([f'R{num}/{who}/brand dimension/{key}', json.dumps(dict(zip(SUMMARY, stats(values))))])
        for key, value in sorted(r.get('gates', {}).items()):
            notes.append([f'R{num}/{who}/gate/{key}', json.dumps(value, ensure_ascii=False, sort_keys=True)])
    for path, digest in sorted(frozen_reports.items()):
        notes.append(['Publisher SHA256/' + path.name, digest])
    for num in sorted(numbers):
        opus = reports.get(('opus', num))
        accepted = Decimal(rounded(opus['mean'])) >= Decimal('8.0') if opus else 'Pending'
        for who in REVIEWERS:
            result = reports.get((who, num))
            status = 'Published' if result else ('Unused; quota unavailable; gated on both primary approvals' if who == 'gemini' else 'Pending')
            verdict = result['report']['verdict'] if result else {}
            progression.append([num, num == current, who, status,
                                *stats(result['totals'] if result else []),
                                verdict.get('satisfied', 'N/A'), verdict.get('genuinely_satisfied', 'N/A'),
                                accepted if who == 'opus' else 'N/A',
                                accepted if who == 'opus' and num == current else 'N/A'])
        available = [reports[who, num] for who in REVIEWERS if (who, num) in reports]
        for a, b in combinations(available, 2):
            require(a['files'].keys() == b['files'].keys(), f'R{num}: reviewer image sets differ')
            for name in sorted(a['files']):
                for measure, keys in [('Quality', WEIGHTS), ('Brand', BRAND)]:
                    x = a['files'][name]['scores'] if measure == 'Quality' else a['files'][name]['ky_fit']['scores']
                    y = b['files'][name]['scores'] if measure == 'Quality' else b['files'][name]['ky_fit']['scores']
                    for key in keys:
                        av = x[key] if isinstance(x, dict) else None
                        bv = y[key] if isinstance(y, dict) else None
                        spread = abs(av - bv) if av not in NA and bv not in NA else None
                        spreads.append([num, name, a['who'], b['who'], measure, key,
                                        av if av not in NA else 'N/A', bv if bv not in NA else 'N/A',
                                        spread if spread is not None else 'N/A', spread >= 2 if spread is not None else 'N/A'])
    if delivery:
        notes.append(['Delivery metadata (not score authority)', json.dumps(delivery, ensure_ascii=False, sort_keys=True)])
    sheets = dict(Scores=scores, Cohorts=cohorts, Surfaces=surfaces, Progression=progression, Spreads=spreads, Notes=notes)
    # All publisher/inventory validation completes before any output is replaced.
    for name, rows in sheets.items():
        with (P / (name.lower() + '.csv')).open('w', newline='', encoding='utf-8') as stream:
            csv.writer(stream, lineterminator='\n').writerows(rows)
    lines = ['# KyDrive Council score history', '',
             f'Current round: **R{current}**. Covered-set descriptions only; not rankings, whole-product or behavior acceptance.', '',
             'Owner progression uses ONLY current Opus overall quality, rounded half-up to one decimal, >=8.0. '
             'Sol, brand, historical rounds and individual cohort shortfalls do not enter the gate. Owner rejection reopens it.', '',
             '| Round | Current | Reviewer | Status | n | Sum fraction | Sum decimal | Mean fraction | Exact decimal | Half-up | Satisfied | Genuinely satisfied | Opus rule for round | Current gate |',
             '| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | --- | --- | --- | --- |']
    lines.extend('| ' + ' | '.join(map(str, row)) + ' |' for row in progression[1:])
    for name, rows in [('Independent cohort summaries', cohorts), ('Independent surface summaries', surfaces)]:
        lines.extend(['', '## ' + name, '', '| ' + ' | '.join(rows[0]) + ' |', '| ' + ' | '.join(['---'] * len(rows[0])) + ' |'])
        lines.extend('| ' + ' | '.join(map(str, row)) + ' |' for row in rows[1:])
    lines.extend(['', '## Evidence and limits', ''])
    lines.extend('- **' + topic + ':** ' + evidence for topic, evidence in notes[1:12])
    lines.extend(['', 'Workbook: `KyDrive-Council.xlsx`; matching CSVs: `scores.csv`, `cohorts.csv`, `surfaces.csv`, '
                  '`progression.csv`, `spreads.csv`, `notes.csv`. Raw report JSON/Markdown remain authoritative and immutable. '
                  'Spreads require parent/audit adjudication; no adjudication is claimed here.', '',
                  'Reproduce with `python3 docs/ui-drive-eight/build-report.py` from the repository root. '
                  'Every published `opus|sol|gemini-roundN` pair is retained; optional `delivery.json.review_round` adds pending rounds.'])
    (P / 'COUNCIL.md').write_text('\n'.join(lines) + '\n', encoding='utf-8')
    workbook(sheets)
    require(all(sha(path.read_bytes()) == digest for path, digest in frozen_reports.items()), 'Publisher bytes changed during export')
    print(f'Validated {len(reports)} immutable report pairs, {len(artifacts)} R1 artifacts, '
          f'{len(inventories)} inventories; CSV/XLSX data round-trip, ZIP CRC and all XML valid.')
    for who in ('opus', 'sol'):
        if (who, current) in reports:
            print(f'R{current} {who}: {stats(reports[who, current]["totals"])}')


if __name__ == '__main__':
    main()
