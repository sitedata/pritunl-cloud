/// <reference path="../References.d.ts"/>
import * as React from 'react';
import * as CertificateTypes from '../types/CertificateTypes';
import * as OrganizationTypes from '../types/OrganizationTypes';
import CertificatesStore from '../stores/CertificatesStore';
import OrganizationsStore from '../stores/OrganizationsStore';
import * as CertificateActions from '../actions/CertificateActions';
import * as OrganizationActions from '../actions/OrganizationActions';
import NonState from './NonState';
import Certificate from './Certificate';
import Page from './Page';
import PageHeader from './PageHeader';

interface State {
	certificates: CertificateTypes.CertificatesRo;
	organizations: OrganizationTypes.OrganizationsRo;
	disabled: boolean;
}

const css = {
	header: {
		marginTop: '-19px',
	} as React.CSSProperties,
	heading: {
		margin: '19px 0 0 0',
	} as React.CSSProperties,
	button: {
		margin: '8px 0 0 8px',
	} as React.CSSProperties,
	buttons: {
		marginTop: '8px',
	} as React.CSSProperties,
	noCerts: {
		height: 'auto',
	} as React.CSSProperties,
};

export default class Certificates extends React.Component<{}, State> {
	constructor(props: any, context: any) {
		super(props, context);
		this.state = {
			certificates: CertificatesStore.certificates,
			organizations: OrganizationsStore.organizations,
			disabled: false,
		};
	}

	componentDidMount(): void {
		CertificatesStore.addChangeListener(this.onChange);
		OrganizationsStore.addChangeListener(this.onChange);
		CertificateActions.sync();
		OrganizationActions.sync();
	}

	componentWillUnmount(): void {
		CertificatesStore.removeChangeListener(this.onChange);
		OrganizationsStore.removeChangeListener(this.onChange);
	}

	onChange = (): void => {
		this.setState({
			...this.state,
			certificates: CertificatesStore.certificates,
			organizations: OrganizationsStore.organizations,
		});
	}

	render(): JSX.Element {
		let certsDom: JSX.Element[] = [];

		this.state.certificates.forEach((
				cert: CertificateTypes.CertificateRo): void => {
			certsDom.push(<Certificate
				key={cert.id}
				certificate={cert}
				organizations={this.state.organizations}
			/>);
		});

		return <Page>
			<PageHeader>
				<div className="layout horizontal wrap" style={css.header}>
					<h2 style={css.heading}>Certificates</h2>
					<div className="flex"/>
					<div style={css.buttons}>
						<button
							className="bp3-button bp3-intent-success bp3-icon-add"
							style={css.button}
							disabled={this.state.disabled}
							type="button"
							onClick={(): void => {
								this.setState({
									...this.state,
									disabled: true,
								});
								CertificateActions.create(null).then((): void => {
									this.setState({
										...this.state,
										disabled: false,
									});
								}).catch((): void => {
									this.setState({
										...this.state,
										disabled: false,
									});
								});
							}}
						>New</button>
					</div>
				</div>
			</PageHeader>
			<div>
				{certsDom}
			</div>
			<NonState
				hidden={!!certsDom.length}
				iconClass="bp3-icon-endorsed"
				title="No certificates"
				description="Add a new certificate to get started."
			/>
		</Page>;
	}
}
