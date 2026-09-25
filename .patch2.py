import io

p = r'internal/pipeline/pipeline.go'
src = io.open(p, encoding='utf-8', newline='').read()

old1 = '\topts, analyzers, path := d.analysisInputBase(ctx, asset, baseOpts, base)'
new1 = '\topts, analyzers, path, baseErr := d.analysisInputBase(ctx, asset, baseOpts, base)'
assert old1 in src, "base call not found"
src = src.replace(old1, new1, 1)

old2 = '''	if roiErr != nil {
		return nil, nil, xcerr.E(xcerr.CodeValidation, "invalid motion_roi on this asset", roiErr)
	}'''
new2 = '''	if roiErr != nil {
		return baseOpts, base, xcerr.E(xcerr.CodeValidation, "invalid motion_roi on this asset", roiErr).Error(), roiErr
	}'''
assert old2 in src, "roi return not found"
src = src.replace(old2, new2, 1)

old3 = '''	if asset.PlayerSpot == nil {
		return opts, analyzers, path
	}'''
new3 = '''	if asset.PlayerSpot == nil {
		return opts, analyzers, path, nil
	}'''
assert old3 in src, "spot early return not found"
src = src.replace(old3, new3, 1)

old4 = '''	hash := player.SigHash(sig.Bins)
	opts.PlayerSig = hash
	analyzers = append(analyzers, analysis.PlayerPresenceAnalyzer{Sig: sig})
	return opts, analyzers, path
}'''
new4 = '''	hash := player.SigHash(sig.Bins)
	opts.PlayerSig = hash
	analyzers = append(analyzers, analysis.PlayerPresenceAnalyzer{Sig: sig})
	return opts, analyzers, path, nil
}'''
assert old4 in src, "tail return not found"
src = src.replace(old4, new4, 1)

# baseErr must propagate
if "baseErr" in src and "return opts, analyzers, path, nil" in src:
    old5 = '''	opts, analyzers, path, baseErr := d.analysisInputBase(ctx, asset, baseOpts, base)
	// Style-driven extras'''
    new5 = '''	opts, analyzers, path, baseErr := d.analysisInputBase(ctx, asset, baseOpts, base)
	if baseErr != nil {
		return baseOpts, base, path, baseErr
	}
	// Style-driven extras'''
    if old5 not in src:
        old5b = '''	opts, analyzers, path, baseErr := d.analysisInputBase(ctx, asset, baseOpts, base)
'''
        ins = '''	if baseErr != nil {
		return baseOpts, base, path, baseErr
	}
'''
        assert old5b in src
        src = src.replace(old5b, old5b + ins, 1)

io.open(p, 'w', encoding='utf-8', newline='').write(src)
print("patched")
